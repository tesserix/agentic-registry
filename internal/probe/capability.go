// Package probe compares what an MCP server declares in the registry against
// what it actually serves. The manifest stays the operator's declaration and
// the probe stays an observation: they are compared, never merged.
package probe

import (
	"crypto/sha256"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Observation is one probe result for one MCP server.
type Observation struct {
	Reachable       bool
	Tools           []string
	ProtocolVersion string
	Error           string
	ProbedAt        time.Time
}

// StatusFor renders the status document for a server's declaration and the
// probe that observed it. Callers write it to the artifact's status; spec is
// read-only here.
func StatusFor(spec map[string]interface{}, obs Observation) map[string]interface{} {
	declaredTools := declaredToolNames(spec)
	status := map[string]interface{}{
		"declaredHash": hashTools(declaredTools),
		"lastProbedAt": obs.ProbedAt.UTC().Format(time.RFC3339),
	}
	if obs.ProtocolVersion != "" {
		status["protocolVersion"] = obs.ProtocolVersion
	}

	if !obs.Reachable {
		status["conditions"] = []map[string]interface{}{
			condition("Ready", "False", "Unreachable", obs.Error, obs.ProbedAt),
			condition("Unreachable", "True", "ProbeFailed", obs.Error, obs.ProbedAt),
		}
		return status
	}
	if obs.ProtocolVersion != protocolVersion {
		message := fmt.Sprintf("server does not support required protocol %s", protocolVersion)
		status["conditions"] = []map[string]interface{}{
			condition("Ready", "False", "UnsupportedProtocol", message, obs.ProbedAt),
			condition("Unreachable", "False", "Probed", "", obs.ProbedAt),
		}
		return status
	}

	observedTools := normalize(obs.Tools)
	status["observedTools"] = observedTools
	status["observedHash"] = hashTools(observedTools)

	added, removed := diff(declaredTools, observedTools)
	conditions := []map[string]interface{}{
		condition("Ready", "True", "Probed", "", obs.ProbedAt),
		condition("Unreachable", "False", "Probed", "", obs.ProbedAt),
	}
	if len(added) == 0 && len(removed) == 0 {
		conditions = append(conditions, condition("Drifted", "False", "InSync", "", obs.ProbedAt))
		status["conditions"] = conditions
		return status
	}

	conditions = append(conditions,
		condition("Drifted", "True", "CapabilityDrift", driftMessage(added, removed), obs.ProbedAt))
	status["conditions"] = conditions
	if next, ok := nextVersion(spec, len(removed) > 0); ok {
		status["proposedVersion"] = next
	}
	return status
}

// DeclaredHash is the hash of a server's declared tool surface, exposed so
// callers can detect a changed declaration without re-probing.
func DeclaredHash(spec map[string]interface{}) string {
	return hashTools(declaredToolNames(spec))
}

func declaredToolNames(spec map[string]interface{}) []string {
	raw, _ := spec["tools"].([]interface{})
	names := make([]string, 0, len(raw))
	for _, entry := range raw {
		switch t := entry.(type) {
		case string:
			names = append(names, t)
		case map[string]interface{}:
			if n, ok := t["name"].(string); ok {
				names = append(names, n)
			}
		}
	}
	return normalize(names)
}

func normalize(tools []string) []string {
	seen := make(map[string]bool, len(tools))
	out := make([]string, 0, len(tools))
	for _, t := range tools {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func hashTools(tools []string) string {
	return fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(strings.Join(tools, "\n"))))
}

func diff(declared, observed []string) (added, removed []string) {
	inDeclared := make(map[string]bool, len(declared))
	for _, t := range declared {
		inDeclared[t] = true
	}
	inObserved := make(map[string]bool, len(observed))
	for _, t := range observed {
		inObserved[t] = true
		if !inDeclared[t] {
			added = append(added, t)
		}
	}
	for _, t := range declared {
		if !inObserved[t] {
			removed = append(removed, t)
		}
	}
	return added, removed
}

func driftMessage(added, removed []string) string {
	parts := make([]string, 0, 2)
	if len(added) > 0 {
		parts = append(parts, "undeclared tools: "+strings.Join(added, ", "))
	}
	if len(removed) > 0 {
		parts = append(parts, "missing tools: "+strings.Join(removed, ", "))
	}
	return strings.Join(parts, "; ")
}

// nextVersion proposes the semver a human may tag the drift as: removing a
// tool breaks callers, adding one does not. It is a proposal, never applied.
func nextVersion(spec map[string]interface{}, breaking bool) (string, bool) {
	current, _ := spec["version"].(string)
	parts := strings.SplitN(strings.TrimPrefix(current, "v"), ".", 3)
	if len(parts) != 3 {
		return "", false
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil {
		return "", false
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil {
		return "", false
	}
	if breaking {
		return fmt.Sprintf("%d.0.0", major+1), true
	}
	return fmt.Sprintf("%d.%d.0", major, minor+1), true
}

func condition(condType, status, reason, message string, at time.Time) map[string]interface{} {
	c := map[string]interface{}{
		"type":               condType,
		"status":             status,
		"reason":             reason,
		"lastTransitionTime": at.UTC().Format(time.RFC3339),
	}
	if message != "" {
		c["message"] = message
	}
	return c
}
