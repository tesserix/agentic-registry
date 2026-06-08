package render

import (
	"os"
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

func promptObj(spec map[string]interface{}) v1alpha1.Object {
	return v1alpha1.Object{
		Kind: v1alpha1.KindPrompt,
		Spec: spec,
	}
}

// TestPromptPartialTagCannotReadFiles is the regression test for the Mustache
// local-file-inclusion vector: a tenant-authored template using a partial tag
// like {{> /etc/hostname}} must NOT have the file's contents interpolated into
// the rendered output. With an empty StaticProvider the partial resolves to "".
func TestPromptPartialTagCannotReadFiles(t *testing.T) {
	// Use a file we control and know the contents of, so the assertion is exact
	// even on a host where /etc/hostname is empty or absent.
	f, err := os.CreateTemp(t.TempDir(), "secret-*")
	if err != nil {
		t.Fatal(err)
	}
	const secret = "TOP-SECRET-FILE-CONTENTS-DO-NOT-LEAK"
	if _, err := f.WriteString(secret); err != nil {
		t.Fatal(err)
	}
	f.Close()

	cases := []struct {
		name string
		spec map[string]interface{}
	}{
		{"absolute-path-partial", map[string]interface{}{"content": "before {{> " + f.Name() + "}} after"}},
		{"etc-hostname-partial", map[string]interface{}{"content": "host: {{> /etc/hostname}}"}},
		{"template-field-partial", map[string]interface{}{"template": "x {{> " + f.Name() + "}} y"}},
		{"message-partial", map[string]interface{}{
			"messages": []interface{}{
				map[string]interface{}{"role": "system", "content": "{{> " + f.Name() + "}}"},
			},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out, err := Prompt(promptObj(tc.spec), Request{})
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			for _, m := range out.Messages {
				if strings.Contains(m.Content, secret) {
					t.Fatalf("rendered output leaked file contents: %q", m.Content)
				}
				// The partial tag must have resolved to empty, not been left
				// verbatim (which would also be wrong/confusing).
				if strings.Contains(m.Content, "{{>") {
					t.Fatalf("partial tag left unrendered: %q", m.Content)
				}
			}
		})
	}
}

// TestPromptTemplateFieldRenders verifies spec.template is honoured as a synonym
// for spec.content (seeded Prompts use template) and that variables interpolate.
func TestPromptTemplateFieldRenders(t *testing.T) {
	obj := promptObj(map[string]interface{}{
		"template": "Summarize: {{topic}}",
	})
	out, err := Prompt(obj, Request{Variables: map[string]interface{}{"topic": "the outage"}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if len(out.Messages) != 1 {
		t.Fatalf("want 1 message, got %d", len(out.Messages))
	}
	if got := out.Messages[0].Content; got != "Summarize: the outage" {
		t.Fatalf("unexpected render: %q", got)
	}
}

// TestPromptVariableInterpolationStillWorks is a guard that switching to
// RenderPartials did not break ordinary {{var}} interpolation.
func TestPromptVariableInterpolationStillWorks(t *testing.T) {
	obj := promptObj(map[string]interface{}{
		"content": "Hello {{name}}",
	})
	out, err := Prompt(obj, Request{Variables: map[string]interface{}{"name": "world"}})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if got := out.Messages[0].Content; got != "Hello world" {
		t.Fatalf("unexpected render: %q", got)
	}
}
