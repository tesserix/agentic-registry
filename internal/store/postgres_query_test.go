package store

import (
	"strings"
	"testing"

	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// A LatestOnly list must collapse to one row per artifact in SQL. Collapsing in
// Go instead made every list materialize the whole maxScanRows scan — 100k rows
// of spec JSONB — which OOMKilled the server on a catalogue with many revisions.
func TestListScanQueryCollapsesLatestInSQL(t *testing.T) {
	opts := ListOptions{Kind: v1alpha1.KindSkill, Namespace: "devai", LatestOnly: true}
	q, args, _ := listScanQuery(opts, false)

	if !strings.Contains(q, "DISTINCT ON (kind, namespace, name)") {
		t.Fatalf("latest-only scan must collapse in SQL, got: %s", q)
	}
	if !strings.Contains(q, "ORDER BY kind, namespace, name, updated_at DESC") {
		t.Fatalf("collapse must keep the newest revision, got: %s", q)
	}
	if !strings.Contains(q, "deletion_timestamp IS NULL") {
		t.Fatalf("soft-deleted rows must stay excluded, got: %s", q)
	}
	if len(args) != 2 || args[0] != string(v1alpha1.KindSkill) || args[1] != "devai" {
		t.Fatalf("kind/namespace must be bound parameters, got %v", args)
	}
}

func TestListScanQueryKeepsEveryRevisionWhenNotLatestOnly(t *testing.T) {
	q, _, _ := listScanQuery(ListOptions{Kind: v1alpha1.KindSkill}, false)
	if strings.Contains(q, "DISTINCT ON") {
		t.Fatalf("a tag listing must see every revision, got: %s", q)
	}
}

func TestListScanQueryStaysBounded(t *testing.T) {
	for _, latestOnly := range []bool{true, false} {
		q, _, _ := listScanQuery(ListOptions{LatestOnly: latestOnly}, false)
		if !strings.Contains(q, "LIMIT 100000") {
			t.Fatalf("latestOnly=%v: scan must stay bounded, got: %s", latestOnly, q)
		}
	}
}

// Semantic ranking orders the whole candidate set by cosine distance, which a
// per-artifact DISTINCT ON would reorder — so ranked search keeps the Go
// collapse and marks the result pre-ranked.
func TestListScanQueryRanksInsteadOfCollapsingWhenVectorSearching(t *testing.T) {
	q, args, preRanked := listScanQuery(ListOptions{Search: "deploy", LatestOnly: true}, true)
	if strings.Contains(q, "DISTINCT ON") {
		t.Fatalf("ranked search must not collapse in SQL, got: %s", q)
	}
	if !strings.Contains(q, "embedding <=>") {
		t.Fatalf("ranked search must order by cosine distance, got: %s", q)
	}
	if !preRanked {
		t.Fatal("ranked search must report pre-ranked so the pipeline keeps SQL order")
	}
	if len(args) != 1 {
		t.Fatalf("query embedding must be a bound parameter, got %v", args)
	}
}

func TestEmbeddingBackfillRebuildsDocumentsAfterProjectionChanges(t *testing.T) {
	if strings.Contains(strings.ToLower(embeddingBackfillQuery), "embedding is null") {
		t.Fatalf("backfill must refresh existing vectors when the safe search projection changes: %s", embeddingBackfillQuery)
	}
	if !strings.Contains(embeddingBackfillQuery, "registry.artifacts") {
		t.Fatalf("backfill query does not select artifacts: %s", embeddingBackfillQuery)
	}
}
