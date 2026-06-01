package v1alpha1

import (
	"fmt"
	"strings"
)

// acceptedGroups are the apiVersion groups we ingest. devai's seeds carry
// registry.solo.io; the real solo.io aregistry carries ar.dev. We normalize all
// three to our canonical Group on write so existing seeds publish unchanged.
//
// This is the single most important compatibility detail: devai is the anchor
// client and its manifests must apply without edits.
var acceptedGroups = map[string]bool{
	Group:              true,
	"registry.solo.io": true,
	"ar.dev":           true,
	"registry.agentic": true, // tolerate a common typo of our own group
}

// NormalizeAPIVersion validates an incoming apiVersion and returns our
// canonical GroupVersion. An empty apiVersion is tolerated (defaults to ours)
// so minimal manifests work. Unknown groups are rejected.
func NormalizeAPIVersion(apiVersion string) (string, error) {
	if apiVersion == "" {
		return GroupVersion, nil
	}
	group := apiVersion
	if i := strings.LastIndex(apiVersion, "/"); i >= 0 {
		group = apiVersion[:i]
	}
	if !acceptedGroups[group] {
		return "", fmt.Errorf("unsupported apiVersion %q: accepted groups are %s, registry.solo.io, ar.dev",
			apiVersion, Group)
	}
	return GroupVersion, nil
}

// ValidKind reports whether k is a kind we catalog or the accepted Project alias.
func ValidKind(k Kind) bool {
	if k == KindProject {
		return true
	}
	_, ok := pluralByKind[k]
	return ok
}

// Validate checks the envelope is well-formed enough to persist. It normalizes
// apiVersion in place and enforces name/kind presence.
func (o *Object) Validate() error {
	v, err := NormalizeAPIVersion(o.APIVersion)
	if err != nil {
		return err
	}
	o.APIVersion = v
	if o.Kind == "" {
		return fmt.Errorf("kind is required")
	}
	if !ValidKind(o.Kind) {
		return fmt.Errorf("unknown kind %q", o.Kind)
	}
	if strings.TrimSpace(o.Metadata.Name) == "" {
		return fmt.Errorf("metadata.name is required")
	}
	switch o.Metadata.Visibility {
	case "", VisibilityPublic, VisibilityInternal, VisibilityPrivate:
	default:
		return fmt.Errorf("invalid visibility %q (want public|internal|private)", o.Metadata.Visibility)
	}
	// Per-kind spec shape check (lenient: known fields only, unknown pass
	// through). Returns a *SpecError so the API layer can surface field errors.
	if err := ValidateSpec(o.Kind, o.Spec); err != nil {
		return err
	}
	return nil
}
