// Package selector implements the Kubernetes LabelSelector grammar, used for
// runtime discovery of registry artifacts. It supports both the JSON object
// form (matchLabels + matchExpressions) and the URL string form
// ("domain in (code-review),language=go,!deprecated").
//
// Operators are exactly the four LabelSelector operators — In, NotIn, Exists,
// DoesNotExist — ANDed together, with no OR. We deliberately do NOT support
// Gt/Lt (those are node-affinity only) so client intuition stays portable.
//
// SECURITY: the selector is discovery convenience, never an authorization
// boundary. The store applies the visibility/RBAC pre-filter BEFORE evaluating
// any selector.
package selector

import (
	"fmt"
	"strings"
)

type Operator string

const (
	OpIn           Operator = "In"
	OpNotIn        Operator = "NotIn"
	OpExists       Operator = "Exists"
	OpDoesNotExist Operator = "DoesNotExist"
)

// Requirement is a single match expression.
type Requirement struct {
	Key      string   `json:"key"`
	Operator Operator `json:"operator"`
	Values   []string `json:"values,omitempty"`
}

// Selector is a set of requirements, ANDed. The zero value matches everything.
type Selector struct {
	MatchLabels      map[string]string `json:"matchLabels,omitempty"`
	MatchExpressions []Requirement     `json:"matchExpressions,omitempty"`
}

// Empty reports whether the selector imposes no constraints (matches all).
func (s Selector) Empty() bool {
	return len(s.MatchLabels) == 0 && len(s.MatchExpressions) == 0
}

// Matches reports whether labels satisfy every requirement (AND semantics).
func (s Selector) Matches(labels map[string]string) bool {
	for k, v := range s.MatchLabels {
		if labels[k] != v {
			return false
		}
	}
	for _, r := range s.MatchExpressions {
		if !r.matches(labels) {
			return false
		}
	}
	return true
}

func (r Requirement) matches(labels map[string]string) bool {
	val, has := labels[r.Key]
	switch r.Operator {
	case OpExists:
		return has
	case OpDoesNotExist:
		return !has
	case OpIn:
		return has && contains(r.Values, val)
	case OpNotIn:
		// NotIn matches when the key is absent OR its value is not listed.
		return !has || !contains(r.Values, val)
	default:
		return false
	}
}

func contains(vs []string, target string) bool {
	for _, v := range vs {
		if v == target {
			return true
		}
	}
	return false
}

// Parse turns the URL string form into a Selector. Grammar (comma-separated):
//
//	key=value | key==value      -> In [value]
//	key!=value                  -> NotIn [value]
//	key in (a, b, c)            -> In [a b c]
//	key notin (a, b)            -> NotIn [a b]
//	key                         -> Exists
//	!key                        -> DoesNotExist
//
// An empty string yields the match-everything selector.
func Parse(s string) (Selector, error) {
	sel := Selector{MatchLabels: map[string]string{}}
	s = strings.TrimSpace(s)
	if s == "" {
		return Selector{}, nil
	}
	for _, tok := range splitTopLevel(s) {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		req, isEquality, key, val, err := parseToken(tok)
		if err != nil {
			return Selector{}, err
		}
		if isEquality {
			sel.MatchLabels[key] = val
			continue
		}
		sel.MatchExpressions = append(sel.MatchExpressions, req)
	}
	if len(sel.MatchLabels) == 0 {
		sel.MatchLabels = nil
	}
	return sel, nil
}

func parseToken(tok string) (req Requirement, isEquality bool, key, val string, err error) {
	lower := strings.ToLower(tok)
	switch {
	case strings.Contains(lower, " notin "):
		key, vals, e := parseSetExpr(tok, "notin")
		if e != nil {
			return req, false, "", "", e
		}
		return Requirement{Key: key, Operator: OpNotIn, Values: vals}, false, "", "", nil
	case strings.Contains(lower, " in "):
		key, vals, e := parseSetExpr(tok, "in")
		if e != nil {
			return req, false, "", "", e
		}
		return Requirement{Key: key, Operator: OpIn, Values: vals}, false, "", "", nil
	case strings.Contains(tok, "!="):
		k, v := splitOnce(tok, "!=")
		return Requirement{Key: strings.TrimSpace(k), Operator: OpNotIn, Values: []string{strings.TrimSpace(v)}}, false, "", "", nil
	case strings.Contains(tok, "=="):
		k, v := splitOnce(tok, "==")
		return req, true, strings.TrimSpace(k), strings.TrimSpace(v), nil
	case strings.Contains(tok, "="):
		k, v := splitOnce(tok, "=")
		return req, true, strings.TrimSpace(k), strings.TrimSpace(v), nil
	case strings.HasPrefix(tok, "!"):
		return Requirement{Key: strings.TrimSpace(tok[1:]), Operator: OpDoesNotExist}, false, "", "", nil
	default:
		if strings.ContainsAny(tok, "(),") {
			return req, false, "", "", fmt.Errorf("malformed selector token %q", tok)
		}
		return Requirement{Key: tok, Operator: OpExists}, false, "", "", nil
	}
}

// parseSetExpr parses "key in (a, b, c)" / "key notin (a)".
func parseSetExpr(tok, op string) (key string, values []string, err error) {
	idx := strings.Index(strings.ToLower(tok), " "+op+" ")
	key = strings.TrimSpace(tok[:idx])
	rest := strings.TrimSpace(tok[idx+len(op)+2:])
	if !strings.HasPrefix(rest, "(") || !strings.HasSuffix(rest, ")") {
		return "", nil, fmt.Errorf("expected parenthesized value set in %q", tok)
	}
	inner := strings.TrimSuffix(strings.TrimPrefix(rest, "("), ")")
	for _, v := range strings.Split(inner, ",") {
		if v = strings.TrimSpace(v); v != "" {
			values = append(values, v)
		}
	}
	if key == "" || len(values) == 0 {
		return "", nil, fmt.Errorf("empty key or value set in %q", tok)
	}
	return key, values, nil
}

// splitTopLevel splits on commas that are not inside parentheses.
func splitTopLevel(s string) []string {
	var parts []string
	depth, start := 0, 0
	for i, r := range s {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func splitOnce(s, sep string) (string, string) {
	i := strings.Index(s, sep)
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+len(sep):]
}
