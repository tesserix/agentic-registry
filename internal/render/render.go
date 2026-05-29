// Package render turns a stored Prompt artifact into a ready-to-send provider
// payload. This is the Portkey-style contract that keeps the registry OFF the
// request path: a client fetches a rendered {model, messages, params, tools}
// object and then sends it to whatever gateway/provider it wants. The registry
// never proxies the LLM call.
//
// Templating is logic-less Mustache so tenant-authored templates cannot execute
// arbitrary logic — important for a multi-tenant, self-hosted registry.
package render

import (
	"fmt"

	"github.com/cbroglie/mustache"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// Request carries the variables and optional parameter overrides.
type Request struct {
	Variables map[string]interface{} `json:"variables"`
	Overrides map[string]interface{} `json:"overrides,omitempty"`
}

// Message is one chat message in the rendered payload.
type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// Rendered is the ready-to-send provider payload.
type Rendered struct {
	Model    string                 `json:"model,omitempty"`
	Messages []Message              `json:"messages"`
	Params   map[string]interface{} `json:"params,omitempty"`
	Tools    []interface{}          `json:"tools,omitempty"`
}

// Prompt renders obj.spec against the request variables.
//
// Expected Prompt spec shape (all optional except messages or content):
//
//	spec:
//	  model: "claude-opus-4-8"
//	  messages: [{role: system, content: "You are {{persona}}."}]
//	  # OR a single inline string:
//	  content: "Summarize {{topic}}."
//	  params: {temperature: 0.2}
//	  tools: [...]
func Prompt(obj v1alpha1.Object, req Request) (Rendered, error) {
	if obj.Kind != v1alpha1.KindPrompt {
		return Rendered{}, fmt.Errorf("not a Prompt: %s", obj.Kind)
	}
	vars := req.Variables
	if vars == nil {
		vars = map[string]interface{}{}
	}
	spec := obj.Spec

	out := Rendered{Params: map[string]interface{}{}}
	if m, ok := spec["model"].(string); ok {
		out.Model = m
	}

	// messages[] takes precedence; otherwise a single content string becomes a
	// user message.
	if raw, ok := spec["messages"].([]interface{}); ok {
		for _, item := range raw {
			msg, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)
			content, _ := msg["content"].(string)
			rendered, err := mustache.Render(content, vars)
			if err != nil {
				return Rendered{}, err
			}
			out.Messages = append(out.Messages, Message{Role: role, Content: rendered})
		}
	} else if content, ok := spec["content"].(string); ok {
		rendered, err := mustache.Render(content, vars)
		if err != nil {
			return Rendered{}, err
		}
		out.Messages = append(out.Messages, Message{Role: "user", Content: rendered})
	} else {
		return Rendered{}, fmt.Errorf("prompt spec has neither messages[] nor content")
	}

	if params, ok := spec["params"].(map[string]interface{}); ok {
		for k, v := range params {
			out.Params[k] = v
		}
	}
	if tools, ok := spec["tools"].([]interface{}); ok {
		out.Tools = tools
	}
	// Caller overrides win (e.g. temperature, max_tokens).
	for k, v := range req.Overrides {
		if k == "model" {
			if s, ok := v.(string); ok {
				out.Model = s
			}
			continue
		}
		out.Params[k] = v
	}
	if len(out.Params) == 0 {
		out.Params = nil
	}
	return out, nil
}
