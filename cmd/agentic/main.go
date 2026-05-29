// Command agentic is the CLI for the Agentic Registry. It publishes, fetches,
// and scaffolds artifacts against any registry endpoint over the /v0 API.
//
//	agentic login   --registry URL [--token T]   save endpoint + token
//	agentic init    <kind> <name>                scaffold a manifest to stdout
//	agentic apply   -f <file.yaml>               publish a multi-doc YAML bundle
//	agentic push    <file.yaml>                  publish a single resource
//	agentic list    <plural> [--selector S]      list a collection
//	agentic pull    <plural> <name> [--tag T]    fetch one artifact as JSON
//
// The CLI holds no secrets beyond the token the user explicitly saves to
// ~/.agentic/config.json (file mode 0600).
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "login":
		err = cmdLogin(os.Args[2:])
	case "init":
		err = cmdInit(os.Args[2:])
	case "apply":
		err = cmdApply(os.Args[2:])
	case "push":
		err = cmdPush(os.Args[2:])
	case "list":
		err = cmdList(os.Args[2:])
	case "pull":
		err = cmdPull(os.Args[2:])
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
  agentic login --registry URL [--token TOKEN]
  agentic init <kind> <name>            scaffold a manifest (Skill|Tool|MCPServer|Prompt|Workflow|Blueprint|Agent)
  agentic apply -f <file.yaml>          publish a multi-doc YAML bundle
  agentic push <file.yaml>              publish a single resource
  agentic list <plural> [--selector S]  list a collection
  agentic pull <plural> <name> [--tag T]

Environment:
  AGENTIC_REGISTRY   registry base URL (overrides saved config)
  AGENTIC_TOKEN      bearer token (overrides saved config)
`)
}

// ---- config ----------------------------------------------------------------

type config struct {
	Registry string `json:"registry"`
	Token    string `json:"token,omitempty"`
}

func configPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".agentic", "config.json")
}

func loadConfig() config {
	c := config{Registry: "http://localhost:8080"}
	if b, err := os.ReadFile(configPath()); err == nil {
		_ = json.Unmarshal(b, &c)
	}
	if v := os.Getenv("AGENTIC_REGISTRY"); v != "" {
		c.Registry = v
	}
	if v := os.Getenv("AGENTIC_TOKEN"); v != "" {
		c.Token = v
	}
	return c
}

func cmdLogin(args []string) error {
	fs := flags(args)
	c := loadConfig()
	if v := fs["registry"]; v != "" {
		c.Registry = v
	}
	if v := fs["token"]; v != "" {
		c.Token = v
	}
	if err := os.MkdirAll(filepath.Dir(configPath()), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(c, "", "  ")
	if err := os.WriteFile(configPath(), b, 0o600); err != nil {
		return err
	}
	fmt.Printf("saved %s (registry=%s)\n", configPath(), c.Registry)
	return nil
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
	resp, err := request(c, http.MethodPost, "/v0/apply", "application/yaml", body)
	if err != nil {
		return err
	}
	fmt.Println(resp)
	return nil
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
		return fmt.Errorf("usage: agentic list <plural> [--selector S]")
	}
	plural := args[0]
	fs := flags(args[1:])
	c := loadConfig()
	path := "/v0/" + plural
	if sel := fs["selector"]; sel != "" {
		path += "?labelSelector=" + url.QueryEscape(sel)
	}
	resp, err := request(c, http.MethodGet, path, "", nil)
	if err != nil {
		return err
	}
	fmt.Println(resp)
	return nil
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

// ---- http -------------------------------------------------------------------

func request(c config, method, path, contentType string, body []byte) (string, error) {
	req, err := http.NewRequest(method, strings.TrimRight(c.Registry, "/")+path, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	client := &http.Client{Timeout: 30 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(res.Body)
	if res.StatusCode >= 400 {
		return "", fmt.Errorf("%s %s: %s", method, path, strings.TrimSpace(string(out)))
	}
	return prettyJSON(out), nil
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
