// Command agentic is the CLI for the Agentic Registry. It publishes, fetches,
// and scaffolds artifacts against any registry endpoint over the /v0 API.
//
//	agentic auth login --registry URL             sign in with OAuth 2.1
//	agentic init    <kind> <name>                scaffold a manifest to stdout
//	agentic apply   -f <file.yaml>               publish a multi-doc YAML bundle
//	agentic push    <file.yaml>                  publish a single resource
//	agentic list    <plural> [--selector S]      list a collection
//	agentic pull    <plural> <name> [--tag T]    fetch one artifact as JSON
//
// OAuth refresh material is kept in the operating-system credential store.
package main

import (
	"bytes"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/tesserix/agentic-registry/adapters/agentgateway"
	"github.com/tesserix/agentic-registry/adapters/kagent"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
	"gopkg.in/yaml.v3"
)

// Version is set at build time via -ldflags (GoReleaser).
var Version = "dev"

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "auth":
		err = cmdAuth(os.Args[2:])
	case "login":
		fmt.Fprintln(os.Stderr, "warning: 'agentic login' is deprecated; use 'agentic auth login'")
		err = cmdAuthLogin(os.Args[2:])
	case "init":
		err = cmdInit(os.Args[2:])
	case "apply":
		err = cmdApply(os.Args[2:])
	case "validate":
		err = cmdValidate(os.Args[2:])
	case "push":
		err = cmdPush(os.Args[2:])
	case "list":
		err = cmdList(os.Args[2:])
	case "pull":
		err = cmdPull(os.Args[2:])
	case "versions":
		err = cmdVersions(os.Args[2:])
	case "history":
		err = cmdHistory(os.Args[2:])
	case "search":
		err = cmdSearch(os.Args[2:])
	case "render":
		err = cmdRender(os.Args[2:])
	case "verify":
		err = cmdVerify(os.Args[2:])
	case "delete", "rm":
		err = cmdDelete(os.Args[2:])
	case "status":
		err = cmdStatus(os.Args[2:])
	case "export":
		err = cmdExport(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Printf("agentic %s\n", Version)
		return
	case "-h", "--help", "help":
		usage()
		return
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n", os.Args[1])
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Print(`agentic — Agentic Registry CLI

Usage:
  agentic auth login --registry URL     sign in with device authorization or PKCE
  agentic auth status                   show sign-in state without printing tokens
  agentic auth logout                   remove locally stored OAuth credentials
  agentic init <kind> <name>            scaffold a manifest (Skill|Tool|MCPServer|Prompt|Workflow|Blueprint|Agent)
  agentic apply -f <file.yaml>          publish a multi-doc YAML bundle
  agentic apply -f <file.yaml> --dry-run  validate remotely without writing
  agentic validate -f <file.yaml>       validate a bundle locally
  agentic push <file.yaml>              publish a single resource
  agentic list <plural> [--selector S]  list a collection (table; -o json for raw)
  agentic pull <plural> <name> [--tag T]   fetch one artifact (omit --tag for latest)
  agentic versions <plural> <name>      list all published versions (tags)
  agentic history <plural> <name>       show the append-only revision timeline
  agentic search <query>                cross-kind ranked search
  agentic render <name> [-f vars.json]  render a Prompt with variables
  agentic verify <plural> <name> [--tag T]   verify the registry's signature
  agentic delete <plural> <name> <tag>  delete one version
  agentic status                        registry endpoint, health, signing key
  agentic export <target> <agent>       render an agent for a runtime (target: kagent | agentgateway)
  agentic version                       print the CLI version

Output: add -o json (or --json) to list / search / versions / history for raw JSON.

Environment:
  AGENTIC_REGISTRY   registry base URL (overrides saved config)
  AGENTIC_TOKEN      ephemeral bearer token override (never persisted)
  AGENTIC_CLIENT_ID / AGENTIC_CLIENT_SECRET / AGENTIC_TOKEN_URL
                    CI client-credentials flow (environment only)
  AGENTIC_INSECURE   skip TLS verification (local self-signed gateways only)
`)
}

// ---- init -------------------------------------------------------------------

func cmdInit(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: agentic init <kind> <name>")
	}
	kind, name := args[0], args[1]
	tmpl := fmt.Sprintf(`apiVersion: registry.agentic.dev/v1alpha1
kind: %s
metadata:
  name: %s
  visibility: public
  labels:
    domain: example
spec:
  title: %s
  description: TODO describe this %s.
`, kind, name, name, strings.ToLower(kind))
	fmt.Print(tmpl)
	return nil
}

// ---- apply / push -----------------------------------------------------------

func cmdApply(args []string) error {
	fs := flags(args)
	file := fs["f"]
	if file == "" {
		return fmt.Errorf("usage: agentic apply -f <file.yaml>")
	}
	body, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	c := loadConfig()
	path := "/v0/apply"
	if _, ok := fs["dry-run"]; ok {
		path += "?dryRun=true"
	}
	idempotencyKey := fs["idempotency-key"]
	if idempotencyKey == "" {
		idempotencyKey = uuid.NewString()
	}
	resp, err := requestWithHeaders(c, http.MethodPost, path, "application/yaml", body, map[string]string{
		"Idempotency-Key": idempotencyKey,
	})
	if err != nil {
		return err
	}
	fmt.Println(resp)
	return nil
}

func cmdValidate(args []string) error {
	file := flags(args)["f"]
	if file == "" {
		return fmt.Errorf("usage: agentic validate -f <file.yaml>")
	}
	body, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	count, err := validateBundle(body)
	if err != nil {
		return err
	}
	fmt.Printf("valid: %d resource(s)\n", count)
	return nil
}

func validateBundle(body []byte) (int, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	count := 0
	for {
		var obj v1alpha1.Object
		err := decoder.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			return 0, fmt.Errorf("document %d: invalid YAML: %w", count+1, err)
		}
		if obj.Kind == "" && obj.Metadata.Name == "" {
			continue
		}
		count++
		if err := obj.Validate(); err != nil {
			return 0, fmt.Errorf("document %d: %w", count, err)
		}
	}
	if count == 0 {
		return 0, fmt.Errorf("no resources found in body")
	}
	return count, nil
}

func cmdPush(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: agentic push <file.yaml>")
	}
	// push reuses /v0/apply (which accepts a single doc too).
	return cmdApply([]string{"-f", args[0]})
}

// ---- list / pull ------------------------------------------------------------

func cmdList(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: agentic list <plural> [--selector S] [-o json]")
	}
	plural := args[0]
	fs := flags(args[1:])
	c := loadConfig()
	path := "/v0/" + plural
	q := url.Values{}
	if sel := fs["selector"]; sel != "" {
		q.Set("labelSelector", sel)
	}
	if s := fs["search"]; s != "" {
		q.Set("search", s)
	}
	if qs := q.Encode(); qs != "" {
		path += "?" + qs
	}
	raw, err := requestRaw(c, http.MethodGet, path, "", nil)
	if err != nil {
		return err
	}
	return printArtifacts(raw, wantJSON(fs))
}

func cmdPull(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: agentic pull <plural> <name> [--tag T]")
	}
	plural, name := args[0], args[1]
	fs := flags(args[2:])
	c := loadConfig()
	path := "/v0/" + plural + "/" + url.PathEscape(name)
	if tag := fs["tag"]; tag != "" {
		path += "/" + url.PathEscape(tag)
	}
	resp, err := request(c, http.MethodGet, path, "", nil)
	if err != nil {
		return err
	}
	fmt.Println(resp)
	return nil
}

// ---- versions / history / search / render -----------------------------------

func cmdVersions(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: agentic versions <plural> <name>")
	}
	fs := flags(args[2:])
	c := loadConfig()
	raw, err := requestRaw(c, http.MethodGet, "/v0/"+args[0]+"/"+url.PathEscape(args[1])+"/tags", "", nil)
	if err != nil {
		return err
	}
	return printVersions(raw, wantJSON(fs))
}

func cmdHistory(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: agentic history <plural> <name>")
	}
	fs := flags(args[2:])
	c := loadConfig()
	raw, err := requestRaw(c, http.MethodGet, "/v0/"+args[0]+"/"+url.PathEscape(args[1])+"/revisions", "", nil)
	if err != nil {
		return err
	}
	return printRevisions(raw, wantJSON(fs))
}

func cmdSearch(args []string) error {
	// Leading positionals form the query; flags follow.
	i := 0
	for i < len(args) && !strings.HasPrefix(args[i], "-") {
		i++
	}
	query := strings.Join(args[:i], " ")
	fs := flags(args[i:])
	if query == "" {
		return fmt.Errorf("usage: agentic search <query> [-o json]")
	}
	c := loadConfig()
	raw, err := requestRaw(c, http.MethodGet, "/v0/search?q="+url.QueryEscape(query), "", nil)
	if err != nil {
		return err
	}
	return printArtifacts(raw, wantJSON(fs))
}

func cmdDelete(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("usage: agentic delete <plural> <name> <tag>")
	}
	plural, name, tag := args[0], args[1], args[2]
	c := loadConfig()
	if _, err := requestRaw(c, http.MethodDelete, "/v0/"+plural+"/"+url.PathEscape(name)+"/"+url.PathEscape(tag), "", nil); err != nil {
		return err
	}
	fmt.Printf("deleted %s/%s@%s\n", plural, name, tag)
	return nil
}

func cmdStatus(_ []string) error {
	c := loadConfig()
	fmt.Printf("registry  %s\n", strings.TrimRight(c.Registry, "/"))

	var health struct{ Status, Version, Platform string }
	if raw, err := requestRaw(c, http.MethodGet, "/healthz", "", nil); err == nil {
		_ = json.Unmarshal(raw, &health)
		fmt.Printf("health    %s · %s · store=%s\n", dash(health.Status), dash(health.Version), dash(health.Platform))
	} else {
		fmt.Printf("health    unreachable (%v)\n", err)
		return nil
	}

	var key struct {
		Enabled bool   `json:"enabled"`
		KeyID   string `json:"keyId"`
	}
	if raw, err := requestRaw(c, http.MethodGet, "/v0/signing-key", "", nil); err == nil {
		_ = json.Unmarshal(raw, &key)
		if key.Enabled {
			fmt.Printf("signing   ed25519 · key %s\n", key.KeyID)
		} else {
			fmt.Printf("signing   disabled\n")
		}
	}
	return nil
}

func cmdRender(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: agentic render <name> [-f vars.json]")
	}
	name := args[0]
	fs := flags(args[1:])
	body := []byte(`{"variables":{}}`)
	if f := fs["f"]; f != "" {
		b, err := os.ReadFile(f)
		if err != nil {
			return err
		}
		body = b
	}
	c := loadConfig()
	resp, err := request(c, http.MethodPost, "/v0/prompts/"+url.PathEscape(name)+"/render", "application/json", body)
	if err != nil {
		return err
	}
	fmt.Println(resp)
	return nil
}

// ---- verify -----------------------------------------------------------------

// cmdVerify fetches an artifact + the registry's public signing key and checks
// the Ed25519 signature over the artifact's digest — the same attestation the
// web UI verifies, but from the CLI.
func cmdVerify(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: agentic verify <plural> <name> [--tag T]")
	}
	plural, name := args[0], args[1]
	fs := flags(args[2:])
	c := loadConfig()

	path := "/v0/" + plural + "/" + url.PathEscape(name)
	if tag := fs["tag"]; tag != "" {
		path += "/" + url.PathEscape(tag)
	}
	artRaw, err := requestRaw(c, http.MethodGet, path, "", nil)
	if err != nil {
		return err
	}
	var meta struct {
		Metadata struct {
			Name      string `json:"name"`
			Tag       string `json:"tag"`
			Digest    string `json:"digest"`
			Signature string `json:"signature"`
			SignedBy  string `json:"signedBy"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(artRaw, &meta); err != nil {
		return fmt.Errorf("parse artifact: %w", err)
	}
	if meta.Metadata.Signature == "" {
		return fmt.Errorf("artifact %s is not signed (registry signing disabled)", name)
	}

	keyRaw, err := requestRaw(c, http.MethodGet, "/v0/signing-key", "", nil)
	if err != nil {
		return err
	}
	var key struct {
		Enabled   bool   `json:"enabled"`
		KeyID     string `json:"keyId"`
		PublicKey string `json:"publicKey"`
	}
	if err := json.Unmarshal(keyRaw, &key); err != nil || !key.Enabled {
		return fmt.Errorf("registry has no signing key")
	}
	pub, err := base64.StdEncoding.DecodeString(key.PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("invalid public key")
	}
	sig, err := base64.StdEncoding.DecodeString(meta.Metadata.Signature)
	if err != nil {
		return fmt.Errorf("invalid signature encoding")
	}
	if ed25519.Verify(pub, []byte(meta.Metadata.Digest), sig) {
		fmt.Printf("✓ verified  %s@%s\n  digest %s\n  signed by registry key %s\n", meta.Metadata.Name, meta.Metadata.Tag, meta.Metadata.Digest, key.KeyID)
		return nil
	}
	return fmt.Errorf("✗ signature INVALID for %s@%s", meta.Metadata.Name, meta.Metadata.Tag)
}

// ---- http -------------------------------------------------------------------

// resolvedAgent mirrors the /v0/agents/{name}/resolved response (api.ResolvedAgent)
// so the CLI can feed the export adapters without importing internal/api.
type resolvedAgent struct {
	Agent      v1alpha1.Object              `json:"agent"`
	Resolved   map[string][]v1alpha1.Object `json:"resolved"`
	Unresolved []struct {
		Kind, Ref, Reason string
	} `json:"unresolved,omitempty"`
}

// cmdExport renders a registry Agent for a runtime control plane. It fetches
// the agent's resolved composition (so MCP servers etc. are already looked up)
// and hands it to the kagent / agentgateway adapter, printing Kubernetes YAML.
//
//	agentic export kagent <agent> [--namespace NS] [--model-config REF] [--gateway-url URL]
//	agentic export agentgateway <agent> [--namespace NS] [--gateway NAME] [--path-prefix /mcp]
func cmdExport(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: agentic export <kagent|agentgateway> <agent> [flags]")
	}
	target, name := args[0], args[1]
	fs := flags(args[2:])

	c := loadConfig()
	raw, err := requestRaw(c, http.MethodGet, "/v0/agents/"+url.PathEscape(name)+"/resolved", "", nil)
	if err != nil {
		return err
	}
	var ra resolvedAgent
	if err := json.Unmarshal(raw, &ra); err != nil {
		return fmt.Errorf("decode resolved agent: %w", err)
	}
	for _, u := range ra.Unresolved {
		fmt.Fprintf(os.Stderr, "warning: unresolved %s %q (%s)\n", u.Kind, u.Ref, u.Reason)
	}
	mcpServers := ra.Resolved["mcpServers"]

	var out []byte
	switch target {
	case "agentgateway", "gateway":
		out, err = agentgateway.Build(mcpServers, agentgateway.Options{
			Namespace:        fs["namespace"],
			GatewayName:      fs["gateway"],
			GatewayNamespace: fs["gateway-namespace"],
			PathPrefix:       fs["path-prefix"],
		})
	case "kagent":
		out, err = kagent.Build(ra.Agent, mcpServers, kagent.Options{
			Namespace:      fs["namespace"],
			ModelConfigRef: fs["model-config"],
			GatewayURL:     fs["gateway-url"],
		})
	default:
		return fmt.Errorf("unknown export target %q (want: kagent | agentgateway)", target)
	}
	if err != nil {
		return err
	}
	os.Stdout.Write(out)
	return nil
}

func request(c config, method, path, contentType string, body []byte) (string, error) {
	return requestWithHeaders(c, method, path, contentType, body, nil)
}

func requestWithHeaders(c config, method, path, contentType string, body []byte, headers map[string]string) (string, error) {
	out, err := requestRawWithHeaders(c, method, path, contentType, body, headers)
	if err != nil {
		return "", err
	}
	return prettyJSON(out), nil
}

func requestRaw(c config, method, path, contentType string, body []byte) ([]byte, error) {
	return requestRawWithHeaders(c, method, path, contentType, body, nil)
}

func requestRawWithHeaders(c config, method, path, contentType string, body []byte, headers map[string]string) ([]byte, error) {
	req, err := http.NewRequest(method, strings.TrimRight(c.Registry, "/")+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	token, err := resolveAccessToken(c)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	// AGENTIC_INSECURE skips TLS verification — for local sandboxes behind a
	// self-signed/local-CA gateway. Never set it against a real registry.
	if os.Getenv("AGENTIC_INSECURE") != "" {
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return nil, fmt.Errorf("%s %s: %s", method, path, strings.TrimSpace(string(out)))
	}
	return out, nil
}

func prettyJSON(b []byte) string {
	var v interface{}
	if json.Unmarshal(b, &v) == nil {
		if pretty, err := json.MarshalIndent(v, "", "  "); err == nil {
			return string(pretty)
		}
	}
	return string(b)
}

// flags parses simple "--key value" / "-key value" pairs. Bare values are
// ignored (positional args are read before calling flags()).
func flags(args []string) map[string]string {
	out := map[string]string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			continue
		}
		key := strings.TrimLeft(a, "-")
		val := ""
		if eq := strings.Index(key, "="); eq >= 0 {
			val = key[eq+1:]
			key = key[:eq]
		} else if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			val = args[i+1]
			i++
		}
		out[key] = val
	}
	return out
}
