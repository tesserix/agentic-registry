package store

import (
	"fmt"
	"regexp"
	"strconv"
)

var semverRe = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)$`)

// nextVersion returns the next semantic version (patch bump of the highest
// existing vMAJOR.MINOR.PATCH tag) for an artifact, or "v0.0.1" when it has no
// semver tags yet. Non-semver tags (e.g. user-set "stable") are ignored for
// the max calculation. The result is always unique for the artifact.
func nextVersion(tags []string) string {
	maxMaj, maxMin, maxPatch := 0, 0, 0
	found := false
	for _, t := range tags {
		m := semverRe.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		maj, _ := strconv.Atoi(m[1])
		min, _ := strconv.Atoi(m[2])
		pat, _ := strconv.Atoi(m[3])
		if !found || maj > maxMaj || (maj == maxMaj && (min > maxMin || (min == maxMin && pat > maxPatch))) {
			maxMaj, maxMin, maxPatch = maj, min, pat
			found = true
		}
	}
	if !found {
		return "v0.0.1"
	}
	return fmt.Sprintf("v%d.%d.%d", maxMaj, maxMin, maxPatch+1)
}

// autoVersionRequested reports whether a publish left the version unspecified
// (empty or the legacy "latest"), meaning the store should auto-assign one.
func autoVersionRequested(tag string) bool {
	return tag == "" || tag == "latest"
}
