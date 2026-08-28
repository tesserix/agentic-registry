// Package embed turns an artifact's searchable text into a fixed-length,
// L2-normalized vector so the store can rank results by cosine similarity
// (pgvector's `<=>` operator) instead of plain substring matching.
//
// The default embedder is deterministic and dependency-free: it uses the
// hashing trick over word tokens AND character trigrams, so it needs no model
// server and works the moment the binary starts. Word tokens give lexical
// relevance; character trigrams add fuzzy/typo tolerance ("kubernets" still
// finds "kubernetes"). It is not a learned semantic model — it is a fast,
// portable lexical embedding — but it slots into the exact same vector column
// a learned model would, so swapping in real embeddings later is a one-function
// change with no schema or query churn.
package embed

import (
	"hash/fnv"
	"math"
	"strings"
	"unicode"

	"github.com/tesserix/agentic-registry/internal/discovery"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// Dim is the embedding dimensionality. Fixed at the pgvector column width, so
// changing it requires a schema migration (the store guards against a mismatch).
const Dim = 256

// Text computes the normalized embedding of an arbitrary string (used for the
// query side of a search).
func Text(s string) []float32 {
	v := make([]float64, Dim)
	addFeatures(v, s, 1.0)
	return normalize(v)
}

// SearchText returns the secret-safe capability document used by both vector
// and fallback lexical search.
func SearchText(o v1alpha1.Object) string { return discovery.Text(o) }

// Object builds the embedding of an artifact from the fields a user actually
// searches by. Name and title are weighted higher than the description and
// label values so an exact-name query ranks the right artifact first.
func Object(o v1alpha1.Object) []float32 {
	v := make([]float64, Dim)
	addFeatures(v, SearchText(o), 1.0)
	// Preserve the established identity weighting on top of the richer safe
	// document so exact-name searches remain deterministic.
	addFeatures(v, o.Metadata.Name, 2.0)
	if t, ok := o.Spec["title"].(string); ok {
		addFeatures(v, t, 2.0)
	}
	return normalize(v)
}

// addFeatures folds the word tokens and their character trigrams of s into the
// accumulator with the given weight, using a signed hashing trick.
func addFeatures(v []float64, s string, weight float64) {
	for _, tok := range tokenize(s) {
		bucket(v, "w:"+tok, weight)
		// Pad short tokens so trigrams cover word boundaries.
		padded := "^" + tok + "$"
		for i := 0; i+3 <= len(padded); i++ {
			bucket(v, "t:"+padded[i:i+3], weight*0.5)
		}
	}
}

func bucket(v []float64, feature string, weight float64) {
	h := fnv.New32a()
	_, _ = h.Write([]byte(feature))
	sum := h.Sum32()
	idx := int(sum % Dim)
	// Sign bit decorrelates collisions so unrelated features don't always add.
	if sum&0x80000000 != 0 {
		v[idx] -= weight
	} else {
		v[idx] += weight
	}
}

func tokenize(s string) []string {
	return strings.FieldsFunc(strings.ToLower(s), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
}

func normalize(v []float64) []float32 {
	var norm float64
	for _, x := range v {
		norm += x * x
	}
	out := make([]float32, len(v))
	if norm == 0 {
		return out // zero vector — no features; cosine distance is undefined, sorts last
	}
	inv := 1.0 / math.Sqrt(norm)
	for i, x := range v {
		out[i] = float32(x * inv)
	}
	return out
}
