package resolve

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// httpSource resolves a tool from a configurable HTTP catalog. The URL template
// contains "{name}", substituted with the wire name; a 200 JSON body is mapped
// into a Tool spec (description + input schema, accepting common field names so
// it works against officialskills.sh, an MCP registry, or any plain JSON tool
// catalog without a bespoke adapter each time). 404 → ErrNotFound (try next).
type httpSource struct {
	name    string
	urlTmpl string
	client  *http.Client
}

// NewHTTPSource builds a generic-HTTP Source. `name` is the provenance label
// (recorded as resolve.devai.io/cached-from); `urlTmpl` must contain "{name}".
func NewHTTPSource(name, urlTmpl string) Source {
	return &httpSource{
		name:    name,
		urlTmpl: urlTmpl,
		client: &http.Client{
			Timeout: 10 * time.Second,
			// SSRF hardening: refuse to follow redirects. A configured upstream
			// catalog is trusted, but a 3xx from it could point the client at an
			// arbitrary (e.g. internal/metadata) URL. http.ErrUseLastResponse
			// returns the redirect response itself instead of following it; the
			// non-200 status then maps to an error in Resolve.
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// SourcesFromConfig builds the upstream Source chain from config entries of the
// form "name=urlTemplate" (or a bare urlTemplate, whose host becomes the name).
func SourcesFromConfig(entries []string) []Source {
	out := make([]Source, 0, len(entries))
	for _, e := range entries {
		name, tmpl := "", strings.TrimSpace(e)
		if i := strings.Index(tmpl, "="); i > 0 {
			name, tmpl = tmpl[:i], tmpl[i+1:]
		}
		if tmpl == "" {
			continue
		}
		if name == "" {
			if u, err := url.Parse(tmpl); err == nil && u.Host != "" {
				name = u.Host
			} else {
				name = "http"
			}
		}
		out = append(out, NewHTTPSource(name, tmpl))
	}
	return out
}

func (h *httpSource) Name() string { return h.name }

func (h *httpSource) Resolve(ctx context.Context, ref Ref) (*v1alpha1.Object, error) {
	target := strings.ReplaceAll(h.urlTmpl, "{name}", url.PathEscape(ref.Name))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return nil, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s: http %d", h.name, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, err
	}
	var raw map[string]interface{}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("%s: bad json: %w", h.name, err)
	}
	return mapToTool(raw), nil
}

// mapToTool maps a flexible upstream JSON tool into a Tool spec, accepting the
// common field-name variants seen across catalogs.
func mapToTool(raw map[string]interface{}) *v1alpha1.Object {
	spec := map[string]interface{}{}
	for _, k := range []string{"description", "summary"} {
		if d, ok := raw[k].(string); ok && d != "" {
			spec["description"] = d
			break
		}
	}
	for _, k := range []string{"inputSchema", "input_schema", "parameters", "schema"} {
		if v, ok := raw[k]; ok && v != nil {
			spec["inputSchema"] = v
			break
		}
	}
	for _, k := range []string{"displayName", "title", "name"} {
		if t, ok := raw[k].(string); ok && t != "" {
			spec["displayName"] = t
			break
		}
	}
	return &v1alpha1.Object{Kind: v1alpha1.KindTool, Spec: spec}
}
