package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/tesserix/agentic-registry/internal/activation"
	"github.com/tesserix/agentic-registry/internal/embed"
	"github.com/tesserix/agentic-registry/pkg/api/v1alpha1"
)

// schemaSQL is the embedded, idempotent schema for STANDALONE / OSS self-hosting
// (docker-compose, anyone running the binary). It is applied on connect when
// AUTO_MIGRATE != "false".
//
// In the Tesserix production deployment this same schema is also owned by the
// tesserix-k8s db-schema-bootstrap CronJob (the single source of truth); there
// AUTO_MIGRATE is set to "false" and these statements never run. IF NOT EXISTS
// keeps both paths safe and idempotent.
const schemaSQL = `
CREATE SCHEMA IF NOT EXISTS registry;
CREATE TABLE IF NOT EXISTS registry.artifacts (
    kind               text        NOT NULL,
    namespace          text        NOT NULL,
    name               text        NOT NULL,
    tag                text        NOT NULL,
    uid                uuid        NOT NULL,
    api_version        text        NOT NULL,
    visibility         text        NOT NULL DEFAULT 'private',
    tenant_id          text        NOT NULL,
    org_id             text,
    team_id            text,
    content_hash       char(64)    NOT NULL,
    labels             jsonb       NOT NULL DEFAULT '{}',
    annotations        jsonb       NOT NULL DEFAULT '{}',
    spec               jsonb       NOT NULL DEFAULT '{}',
    status             jsonb       NOT NULL DEFAULT '{}',
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    deletion_timestamp timestamptz,
    PRIMARY KEY (kind, namespace, name, tag)
);
CREATE INDEX IF NOT EXISTS idx_artifacts_labels    ON registry.artifacts USING gin (labels jsonb_path_ops);
CREATE INDEX IF NOT EXISTS idx_artifacts_kind_ns   ON registry.artifacts (kind, namespace);
CREATE INDEX IF NOT EXISTS idx_artifacts_updated   ON registry.artifacts (updated_at);
CREATE INDEX IF NOT EXISTS idx_artifacts_tenant    ON registry.artifacts (tenant_id);

-- Append-only audit timeline. One immutable row per content change (including
-- overwrites of the floating "latest" tag), so history survives mutation.
CREATE TABLE IF NOT EXISTS registry.artifact_revisions (
    kind         text        NOT NULL,
    namespace    text        NOT NULL,
    name         text        NOT NULL,
    tag          text        NOT NULL,
    revision     bigint      NOT NULL,
    uid          uuid        NOT NULL,
    api_version  text        NOT NULL,
    visibility   text        NOT NULL,
    tenant_id    text        NOT NULL,
    org_id       text,
    team_id      text,
    content_hash char(64)    NOT NULL,
    labels       jsonb       NOT NULL DEFAULT '{}',
    annotations  jsonb       NOT NULL DEFAULT '{}',
    spec         jsonb       NOT NULL DEFAULT '{}',
    status       jsonb       NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (kind, namespace, name, tag, revision)
);
CREATE INDEX IF NOT EXISTS idx_revisions_artifact ON registry.artifact_revisions (kind, namespace, name, created_at DESC);

CREATE TABLE IF NOT EXISTS registry.publish_idempotency (
    actor_scope  text        NOT NULL,
    key          text        NOT NULL,
    request_hash char(64)    NOT NULL,
    result       jsonb       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    PRIMARY KEY (actor_scope, key)
);
CREATE INDEX IF NOT EXISTS idx_publish_idempotency_expiry
    ON registry.publish_idempotency (expires_at);

CREATE TABLE IF NOT EXISTS registry.publish_outbox (
    id           uuid        PRIMARY KEY,
    actor_scope  text        NOT NULL,
    event_type   text        NOT NULL,
    payload      jsonb       NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz
);
CREATE INDEX IF NOT EXISTS idx_publish_outbox_unpublished
    ON registry.publish_outbox (created_at, id) WHERE published_at IS NULL;
`

// backfillRevisionsSQL seeds revision 1 from the current artifacts for any row
// that has no history yet (rows written before the timeline existed). Idempotent.
const backfillRevisionsSQL = `
INSERT INTO registry.artifact_revisions
    (kind, namespace, name, tag, revision, uid, api_version, visibility, tenant_id, org_id, team_id,
     content_hash, labels, annotations, spec, status, created_at)
SELECT a.kind, a.namespace, a.name, a.tag, 1, a.uid, a.api_version, a.visibility, a.tenant_id, a.org_id, a.team_id,
       a.content_hash, a.labels, a.annotations, a.spec, a.status, a.created_at
FROM registry.artifacts a
WHERE NOT EXISTS (
    SELECT 1 FROM registry.artifact_revisions r
    WHERE r.kind=a.kind AND r.namespace=a.namespace AND r.name=a.name AND r.tag=a.tag
);`

// vectorDDL adds the pgvector column + HNSW cosine index. Kept separate from
// schemaSQL because pgvector is optional: if the extension isn't installed the
// store falls back to substring search instead of failing to start. %d is the
// embedding dimensionality so the column width always matches the embedder.
var vectorDDL = fmt.Sprintf(`
CREATE EXTENSION IF NOT EXISTS vector;
ALTER TABLE registry.artifacts ADD COLUMN IF NOT EXISTS embedding vector(%d);
CREATE INDEX IF NOT EXISTS idx_artifacts_embedding
    ON registry.artifacts USING hnsw (embedding vector_cosine_ops);
`, embed.Dim)

// maxScanRows caps how many candidate rows a single List query materializes
// before the in-Go discovery pipeline (RBAC pre-filter → selector → collapse →
// paginate) runs. It is a memory safety ceiling, deliberately far above any
// realistic page or per-namespace count, replacing the unbounded 1<<30 sentinel
// callers used to pass for "all".
const maxScanRows = 100000

// Postgres is the production Store backed by PostgreSQL. spec/status/labels are
// JSONB; identity and scope are promoted to real columns and GIN-indexed.
type Postgres struct {
	pool *pgxpool.Pool
	// vectorEnabled is true when the embedding column exists, enabling
	// pgvector cosine-ranked search. Detected at startup; false => substring.
	vectorEnabled bool
	// immutableTags rejects content changes to an already-published version tag.
	immutableTags bool
	// autoVersion assigns the next semver when a publish omits a version.
	autoVersion bool
}

// NewPostgres connects to dsn, optionally applies the embedded schema, and
// returns a ready Store.
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	p := &Postgres{pool: pool}

	if os.Getenv("AUTO_MIGRATE") != "false" {
		if _, err := pool.Exec(ctx, schemaSQL); err != nil {
			pool.Close()
			return nil, fmt.Errorf("postgres: migrate: %w", err)
		}
		// Seed an initial revision for any pre-existing artifact (idempotent).
		if _, err := pool.Exec(ctx, backfillRevisionsSQL); err != nil {
			log.Printf("agentic-registry: revision backfill: %v", err)
		}
		// Best-effort: enable pgvector. A failure here (extension not bundled)
		// is non-fatal — the registry still serves substring search.
		if os.Getenv("VECTOR_SEARCH") != "false" {
			if _, err := pool.Exec(ctx, vectorDDL); err != nil {
				log.Printf("agentic-registry: pgvector unavailable, using substring search: %v", err)
			}
		}
	}

	// Detect the column regardless of who created it (the binary in OSS mode,
	// or the tesserix-k8s db-schema-bootstrap job in production).
	if os.Getenv("VECTOR_SEARCH") != "false" {
		p.vectorEnabled = p.hasEmbeddingColumn(ctx)
		if p.vectorEnabled {
			if n, err := p.backfillEmbeddings(ctx); err != nil {
				log.Printf("agentic-registry: embedding backfill: %v", err)
			} else if n > 0 {
				log.Printf("agentic-registry: backfilled %d embedding(s)", n)
			}
		}
	}
	return p, nil
}

func (p *Postgres) hasEmbeddingColumn(ctx context.Context) bool {
	var ok bool
	err := p.pool.QueryRow(ctx, `SELECT EXISTS (
		SELECT 1 FROM information_schema.columns
		WHERE table_schema='registry' AND table_name='artifacts' AND column_name='embedding')`).Scan(&ok)
	return err == nil && ok
}

const embeddingBackfillQuery = "SELECT " + cols + " FROM registry.artifacts WHERE deletion_timestamp IS NULL"

// backfillEmbeddings refreshes every live embedding at startup. The safe discovery
// projection evolves independently of artifact content, so only filling NULL
// vectors would leave old rows permanently ranked with stale metadata. This is
// intentionally simple and bounded by the registry-sized catalog.
func (p *Postgres) backfillEmbeddings(ctx context.Context) (int, error) {
	rows, err := p.pool.Query(ctx, embeddingBackfillQuery)
	if err != nil {
		return 0, err
	}
	objs, err := scanRows(rows)
	if err != nil {
		return 0, err
	}
	for _, o := range objs {
		_, err := p.pool.Exec(ctx,
			`UPDATE registry.artifacts SET embedding=$5::vector
			 WHERE kind=$1 AND namespace=$2 AND name=$3 AND tag=$4`,
			string(o.Kind), o.Metadata.Namespace, o.Metadata.Name, o.Metadata.Tag, vecLiteral(embed.Object(o)),
		)
		if err != nil {
			return 0, err
		}
	}
	return len(objs), nil
}

// embeddingUpdate is the ON CONFLICT SET clause fragment that refreshes the
// embedding on re-apply, or empty when vector search is disabled.
func embeddingUpdate(enabled bool) string {
	if enabled {
		return "\n    embedding    = EXCLUDED.embedding,"
	}
	return ""
}

// vecLiteral formats a vector as the pgvector text literal "[v1,v2,...]" so it
// can be bound as a parameter with a ::vector cast (no extra driver dependency).
func vecLiteral(v []float32) string {
	var b strings.Builder
	b.Grow(len(v) * 8)
	b.WriteByte('[')
	for i, x := range v {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(x), 'f', 6, 32))
	}
	b.WriteByte(']')
	return b.String()
}

func (p *Postgres) Apply(ctx context.Context, obj v1alpha1.Object) (v1alpha1.Object, bool, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return v1alpha1.Object{}, false, fmt.Errorf("postgres: begin: %w", err)
	}
	defer tx.Rollback(ctx)
	result, created, err := p.applyTx(ctx, tx, obj)
	if err != nil {
		return v1alpha1.Object{}, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return v1alpha1.Object{}, false, fmt.Errorf("postgres: commit: %w", err)
	}
	return result, created, nil
}

func (p *Postgres) ApplyBatch(ctx context.Context, objs []v1alpha1.Object, opts BatchOptions) (BatchResult, error) {
	tx, err := p.pool.Begin(ctx)
	if err != nil {
		return BatchResult{}, fmt.Errorf("postgres: begin batch: %w", err)
	}
	defer tx.Rollback(ctx)
	if opts.IdempotencyKey != "" {
		if _, err := tx.Exec(ctx,
			`SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
			opts.IdempotencyScope, opts.IdempotencyKey,
		); err != nil {
			return BatchResult{}, fmt.Errorf("postgres: idempotency lock: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`DELETE FROM registry.publish_idempotency
			  WHERE actor_scope=$1 AND key=$2 AND expires_at <= now()`,
			opts.IdempotencyScope, opts.IdempotencyKey,
		); err != nil {
			return BatchResult{}, fmt.Errorf("postgres: expire idempotency: %w", err)
		}
		var previousHash string
		var previousJSON []byte
		err := tx.QueryRow(ctx,
			`SELECT request_hash, result FROM registry.publish_idempotency
			  WHERE actor_scope=$1 AND key=$2`,
			opts.IdempotencyScope, opts.IdempotencyKey,
		).Scan(&previousHash, &previousJSON)
		switch {
		case err == nil:
			if previousHash != opts.RequestHash {
				return BatchResult{}, ErrIdempotencyConflict
			}
			var previous []ApplyResult
			if err := json.Unmarshal(previousJSON, &previous); err != nil {
				return BatchResult{}, fmt.Errorf("postgres: decode idempotency result: %w", err)
			}
			return BatchResult{Items: previous, Replayed: true}, nil
		case !errors.Is(err, pgx.ErrNoRows):
			return BatchResult{}, fmt.Errorf("postgres: read idempotency: %w", err)
		}
	}
	results := make([]ApplyResult, 0, len(objs))
	for _, obj := range objs {
		applied, created, err := p.applyTx(ctx, tx, obj)
		if err != nil {
			return BatchResult{}, err
		}
		results = append(results, ApplyResult{Object: applied, Created: created})
	}
	resultJSON, err := json.Marshal(results)
	if err != nil {
		return BatchResult{}, fmt.Errorf("postgres: encode batch result: %w", err)
	}
	if opts.IdempotencyKey != "" {
		if _, err := tx.Exec(ctx,
			`INSERT INTO registry.publish_idempotency
			 (actor_scope,key,request_hash,result,expires_at)
			 VALUES ($1,$2,$3,$4,now()+interval '24 hours')`,
			opts.IdempotencyScope, opts.IdempotencyKey, opts.RequestHash, resultJSON,
		); err != nil {
			return BatchResult{}, fmt.Errorf("postgres: save idempotency: %w", err)
		}
	}
	evidence := make([]map[string]string, 0, len(results))
	for _, result := range results {
		evidence = append(evidence, map[string]string{
			"kind": string(result.Object.Kind), "namespace": result.Object.Metadata.Namespace,
			"name": result.Object.Metadata.Name, "tag": result.Object.Metadata.Tag,
			"digest": result.Object.Digest(),
		})
	}
	payload, err := json.Marshal(map[string]any{"artifacts": evidence})
	if err != nil {
		return BatchResult{}, fmt.Errorf("postgres: encode outbox: %w", err)
	}
	if _, err := tx.Exec(ctx,
		`INSERT INTO registry.publish_outbox (id,actor_scope,event_type,payload)
		 VALUES ($1,$2,'artifact.batch_applied',$3)`,
		uuid.NewString(), opts.IdempotencyScope, payload,
	); err != nil {
		return BatchResult{}, fmt.Errorf("postgres: save outbox: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return BatchResult{}, fmt.Errorf("postgres: commit batch: %w", err)
	}
	return BatchResult{Items: results}, nil
}

func (p *Postgres) applyTx(ctx context.Context, tx pgx.Tx, obj v1alpha1.Object) (v1alpha1.Object, bool, error) {
	obj = obj.Normalized()
	status := map[string]interface{}{"status": "active"}
	statusJSON, err := json.Marshal(status)
	if err != nil {
		return v1alpha1.Object{}, false, fmt.Errorf("postgres: marshal status: %w", err)
	}
	uid := uuid.NewString()

	// Namespace-scoped name uniqueness across all kinds. Serialize concurrent
	// publishes targeting the same (namespace, name) with a transaction-scoped
	// advisory lock so two different kinds can't both pass the guard and race
	// in. The lock auto-releases on commit/rollback; it is keyed on the
	// (namespace, name) pair, so unrelated publishes never contend.
	if _, err := tx.Exec(ctx,
		`SELECT pg_advisory_xact_lock(hashtext($1), hashtext($2))`,
		obj.Metadata.Namespace, obj.Metadata.Name,
	); err != nil {
		return v1alpha1.Object{}, false, fmt.Errorf("postgres: name lock: %w", err)
	}
	// A live object of a DIFFERENT kind already owning this name blocks the
	// publish; re-publishing the same kind is versioning and is allowed.
	// Soft-deleted rows (deletion_timestamp set) release the name.
	var ownerKind, ownerTenant string
	switch err := tx.QueryRow(ctx,
		`SELECT kind, tenant_id FROM registry.artifacts
		 WHERE namespace=$1 AND name=$2 AND kind<>$3 AND deletion_timestamp IS NULL
		 LIMIT 1`,
		obj.Metadata.Namespace, obj.Metadata.Name, string(obj.Kind),
	).Scan(&ownerKind, &ownerTenant); {
	case err == nil:
		return v1alpha1.Object{}, false, newNameConflict(obj, v1alpha1.Kind(ownerKind), ownerTenant)
	case errors.Is(err, pgx.ErrNoRows):
		// No other kind owns the name — proceed.
	default:
		return v1alpha1.Object{}, false, fmt.Errorf("postgres: name guard: %w", err)
	}

	// Auto-assign the next semver when no explicit version was given — unless
	// the content is byte-identical to the newest live revision, which is a
	// no-op re-apply and must upsert that tag rather than mint a new one. A
	// re-applied seed catalogue otherwise grows by a full copy per publish.
	if p.autoVersion && autoVersionRequested(obj.Metadata.Tag) {
		rows, err := tx.Query(ctx,
			`SELECT tag, content_hash, deletion_timestamp IS NULL AS live FROM registry.artifacts
			 WHERE kind=$1 AND namespace=$2 AND name=$3 ORDER BY updated_at DESC`,
			string(obj.Kind), obj.Metadata.Namespace, obj.Metadata.Name)
		if err != nil {
			return v1alpha1.Object{}, false, fmt.Errorf("postgres: version scan: %w", err)
		}
		var tags []string
		var latestTag, latestHash string
		for rows.Next() {
			var t, h string
			var live bool
			if err := rows.Scan(&t, &h, &live); err != nil {
				rows.Close()
				return v1alpha1.Object{}, false, err
			}
			tags = append(tags, t)
			if live && latestTag == "" {
				latestTag, latestHash = t, h
			}
		}
		rows.Close()
		if reusesVersion(obj, latestTag, latestHash) {
			obj.Metadata.Tag = latestTag
		} else {
			obj.Metadata.Tag = nextVersion(tags)
		}
	}

	// Content hash depends on the (now-resolved) tag.
	hash := obj.ContentHash()
	labels, err := json.Marshal(obj.Metadata.Labels)
	if err != nil {
		return v1alpha1.Object{}, false, fmt.Errorf("postgres: marshal labels: %w", err)
	}
	annos, err := json.Marshal(obj.Metadata.Annotations)
	if err != nil {
		return v1alpha1.Object{}, false, fmt.Errorf("postgres: marshal annotations: %w", err)
	}
	spec, err := json.Marshal(orEmpty(obj.Spec))
	if err != nil {
		return v1alpha1.Object{}, false, fmt.Errorf("postgres: marshal spec: %w", err)
	}

	// Lock the (maybe-existing) row and read its current content hash + owner.
	var existingHash, existingTenant string
	hasExisting := true
	err = tx.QueryRow(ctx,
		`SELECT content_hash, tenant_id FROM registry.artifacts
		 WHERE kind=$1 AND namespace=$2 AND name=$3 AND tag=$4 FOR UPDATE`,
		string(obj.Kind), obj.Metadata.Namespace, obj.Metadata.Name, obj.Metadata.Tag,
	).Scan(&existingHash, &existingTenant)
	if errors.Is(err, pgx.ErrNoRows) {
		hasExisting = false
	} else if err != nil {
		return v1alpha1.Object{}, false, fmt.Errorf("postgres: apply read: %w", err)
	}

	// Cross-tenant overwrite guard: the ON CONFLICT upsert reassigns tenant_id/
	// visibility to the incoming object, so without this a writer on tenant B
	// could hijack tenant A's same-(kind,namespace,name,tag) artifact. Reject
	// when the existing row is owned by a different tenant; re-applying to your
	// own tenant's artifact is normal versioning and proceeds.
	if hasExisting && existingTenant != obj.Metadata.TenantID {
		return v1alpha1.Object{}, false, ErrTenantConflict
	}

	// Immutable version tags: refuse to change a published version's content
	// (the floating "latest" tag is exempt).
	if p.immutableTags && obj.Metadata.Tag != v1alpha1.DefaultTag && hasExisting && existingHash != hash {
		return v1alpha1.Object{}, false, ErrImmutableTag
	}

	// The embedding column ($16) is only included when pgvector is enabled, so
	// the same Apply path works whether or not the column exists.
	embCol, embVal := "", ""
	args := []any{
		string(obj.Kind), obj.Metadata.Namespace, obj.Metadata.Name, obj.Metadata.Tag, uid,
		obj.APIVersion, string(obj.Metadata.Visibility), obj.Metadata.TenantID,
		nullStr(obj.Metadata.OrgID), nullStr(obj.Metadata.TeamID),
		hash, labels, annos, spec, statusJSON,
	}
	if p.vectorEnabled {
		embCol, embVal = ", embedding", ", $16::vector"
		args = append(args, vecLiteral(embed.Object(obj)))
	}

	q := `
INSERT INTO registry.artifacts
    (kind, namespace, name, tag, uid, api_version, visibility, tenant_id, org_id, team_id,
     content_hash, labels, annotations, spec, status` + embCol + `, created_at, updated_at, deletion_timestamp)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15` + embVal + `, now(), now(), NULL)
ON CONFLICT (kind, namespace, name, tag) DO UPDATE SET
    api_version  = EXCLUDED.api_version,
    visibility   = EXCLUDED.visibility,
    tenant_id    = EXCLUDED.tenant_id,
    org_id       = EXCLUDED.org_id,
    team_id      = EXCLUDED.team_id,
    content_hash = EXCLUDED.content_hash,
    labels       = EXCLUDED.labels,
    annotations  = EXCLUDED.annotations,
    spec         = EXCLUDED.spec,` + embeddingUpdate(p.vectorEnabled) + `
    status       = registry.artifacts.status,
    updated_at   = now(),
    deletion_timestamp = NULL
RETURNING (xmax = 0) AS inserted, uid, created_at, updated_at`

	var inserted bool
	var gotUID string
	var createdAt, updatedAt time.Time
	if err := tx.QueryRow(ctx, q, args...).Scan(&inserted, &gotUID, &createdAt, &updatedAt); err != nil {
		return v1alpha1.Object{}, false, fmt.Errorf("postgres: apply: %w", err)
	}

	// Append-only revision on every content change (new or changed hash).
	if !hasExisting || existingHash != hash {
		var nextRev int64
		if err := tx.QueryRow(ctx,
			`SELECT COALESCE(MAX(revision),0)+1 FROM registry.artifact_revisions
			 WHERE kind=$1 AND namespace=$2 AND name=$3 AND tag=$4`,
			string(obj.Kind), obj.Metadata.Namespace, obj.Metadata.Name, obj.Metadata.Tag,
		).Scan(&nextRev); err != nil {
			return v1alpha1.Object{}, false, fmt.Errorf("postgres: revision seq: %w", err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO registry.artifact_revisions
			 (kind,namespace,name,tag,revision,uid,api_version,visibility,tenant_id,org_id,team_id,
			  content_hash,labels,annotations,spec,status,created_at)
			 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,now())`,
			string(obj.Kind), obj.Metadata.Namespace, obj.Metadata.Name, obj.Metadata.Tag, nextRev,
			gotUID, obj.APIVersion, string(obj.Metadata.Visibility), obj.Metadata.TenantID,
			nullStr(obj.Metadata.OrgID), nullStr(obj.Metadata.TeamID),
			hash, labels, annos, spec, statusJSON,
		); err != nil {
			return v1alpha1.Object{}, false, fmt.Errorf("postgres: revision insert: %w", err)
		}
	}

	obj.Metadata.UID = gotUID
	obj.Metadata.ContentHash = hash
	obj.Metadata.CreatedAt = &createdAt
	obj.Metadata.UpdatedAt = &updatedAt
	obj.Status = status
	return obj, inserted, nil
}

// ListRevisions returns the artifact's append-only audit timeline (all tags),
// newest first.
func (p *Postgres) ListRevisions(ctx context.Context, kind v1alpha1.Kind, ns, name string) ([]Revision, error) {
	rows, err := p.pool.Query(ctx,
		`SELECT tag, revision, content_hash, visibility, created_at
		 FROM registry.artifact_revisions
		 WHERE kind=$1 AND namespace=$2 AND name=$3
		 ORDER BY created_at DESC, revision DESC`,
		string(kind), ns, name)
	if err != nil {
		return nil, fmt.Errorf("postgres: revisions: %w", err)
	}
	defer rows.Close()
	var out []Revision
	for rows.Next() {
		var r Revision
		var hash string
		if err := rows.Scan(&r.Tag, &r.Revision, &hash, &r.Visibility, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Digest = "sha256:" + hash
		out = append(out, r)
	}
	return out, rows.Err()
}

func (p *Postgres) Get(ctx context.Context, kind v1alpha1.Kind, ns, name, tag string) (v1alpha1.Object, error) {
	if tag == "" || tag == v1alpha1.DefaultTag {
		const q = `SELECT ` + cols + ` FROM registry.artifacts
			WHERE kind=$1 AND namespace=$2 AND name=$3 AND deletion_timestamp IS NULL
			ORDER BY updated_at DESC LIMIT 1`
		return p.scanOne(ctx, q, string(kind), ns, name)
	}
	const q = `SELECT ` + cols + ` FROM registry.artifacts
		WHERE kind=$1 AND namespace=$2 AND name=$3 AND tag=$4 AND deletion_timestamp IS NULL`
	return p.scanOne(ctx, q, string(kind), ns, name, tag)
}

// listScanQuery builds the bounded candidate scan for List, together with its
// bound arguments and whether the rows come back pre-ranked (SQL cosine order,
// which the Go pipeline must not re-sort).
func listScanQuery(opts ListOptions, vectorEnabled bool) (string, []interface{}, bool) {
	where := "TRUE"
	args := []interface{}{}
	add := func(clause string, v interface{}) {
		args = append(args, v)
		where += fmt.Sprintf(" AND %s$%d", clause, len(args))
	}
	if opts.Kind != "" {
		add("kind=", string(opts.Kind))
	}
	if opts.Namespace != "" && opts.Namespace != "all" {
		add("namespace=", opts.Namespace)
	}
	if !opts.IncludeDeleted {
		where += " AND deletion_timestamp IS NULL"
	}

	// Semantic ranking: when a search query is present and pgvector is enabled,
	// order candidates by cosine distance to the query embedding in SQL and let
	// the pipeline preserve that order (PreRanked) instead of substring-filtering.
	// A per-artifact DISTINCT ON would have to reorder those rows, so ranked
	// search keeps the Go collapse.
	if opts.Search != "" && vectorEnabled {
		args = append(args, vecLiteral(embed.Text(opts.Search)))
		orderBy := fmt.Sprintf(" ORDER BY embedding <=> $%d ASC NULLS LAST", len(args))
		return "SELECT " + cols + " FROM registry.artifacts WHERE " + where + orderBy +
			fmt.Sprintf(" LIMIT %d", maxScanRows), args, true
	}

	// Bound the candidate scan in SQL. The RBAC/selector pre-filter and the exact
	// cursor pagination still run in the Go pipeline (page()), so SQL can't be the
	// precise page boundary — but materializing every row (the previous behavior,
	// driven by callers passing Limit=1<<30) is an unbounded-memory DoS on a large
	// catalog. maxScanRows is a hard ceiling far above any realistic page so it
	// never clips a real result set while capping worst-case memory.
	//
	// LatestOnly collapses to the newest revision in SQL rather than in page():
	// a catalogue re-seeded on every sync holds ~90 superseded revisions per
	// artifact, so scanning them all to discard 99% is what actually exhausts
	// the heap. Note this now picks the newest revision *before* the RBAC,
	// selector and search filters instead of after, so a filter can no longer
	// surface a superseded revision — which is what "latest only" means.
	if opts.LatestOnly {
		return "SELECT DISTINCT ON (kind, namespace, name) " + cols +
			" FROM registry.artifacts WHERE " + where +
			" ORDER BY kind, namespace, name, updated_at DESC" +
			fmt.Sprintf(" LIMIT %d", maxScanRows), args, false
	}
	return "SELECT " + cols + " FROM registry.artifacts WHERE " + where +
		fmt.Sprintf(" LIMIT %d", maxScanRows), args, false
}

func (p *Postgres) List(ctx context.Context, opts ListOptions) (ListResult, error) {
	// v1 strategy: fetch the candidate set by kind/namespace/deletion in SQL,
	// then run the shared discovery pipeline (visibility/RBAC pre-filter →
	// selector → search → collapse → paginate) in Go for identical semantics
	// across backends. GIN-pushdown of the selector is a v2 optimization.
	q, args, preRanked := listScanQuery(opts, p.vectorEnabled)
	opts.PreRanked = preRanked
	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return ListResult{}, fmt.Errorf("postgres: list: %w", err)
	}
	defer rows.Close()
	candidates, err := scanRows(rows)
	if err != nil {
		return ListResult{}, err
	}
	return page(candidates, opts), nil
}

func (p *Postgres) ListTags(ctx context.Context, kind v1alpha1.Kind, ns, name string) ([]string, error) {
	const q = `SELECT tag FROM registry.artifacts
		WHERE kind=$1 AND namespace=$2 AND name=$3 AND deletion_timestamp IS NULL
		ORDER BY updated_at DESC`
	rows, err := p.pool.Query(ctx, q, string(kind), ns, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tags []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		tags = append(tags, t)
	}
	if len(tags) == 0 {
		return nil, ErrNotFound
	}
	return tags, nil
}

func (p *Postgres) Delete(ctx context.Context, kind v1alpha1.Kind, ns, name, tag string) error {
	const q = `UPDATE registry.artifacts SET deletion_timestamp=now(),
		status = jsonb_set(status, '{status}', '"deleted"')
		WHERE kind=$1 AND namespace=$2 AND name=$3 AND tag=$4 AND deletion_timestamp IS NULL`
	ct, err := p.pool.Exec(ctx, q, string(kind), ns, name, tag)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) MergeStatus(ctx context.Context, kind v1alpha1.Kind, ns, name, tag string, patch map[string]interface{}) error {
	patchJSON, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("postgres: marshal status patch: %w", err)
	}
	const q = `UPDATE registry.artifacts SET status = coalesce(status, '{}'::jsonb) || $5::jsonb
		WHERE kind=$1 AND namespace=$2 AND name=$3 AND tag=$4`
	ct, err := p.pool.Exec(ctx, q, string(kind), ns, name, tag, patchJSON)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) ObserveActivation(ctx context.Context, ns, name, tag string, observation activation.Observation) (activation.Status, error) {
	var result activation.Status
	err := pgx.BeginFunc(ctx, p.pool, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `SELECT kind, namespace, name, tag, uid, api_version, visibility, tenant_id, org_id, team_id,
			content_hash, labels, annotations, spec, status, created_at, updated_at, deletion_timestamp
			FROM registry.artifacts WHERE kind=$1 AND namespace=$2 AND name=$3 AND tag=$4 FOR UPDATE`,
			string(v1alpha1.KindMCPServer), ns, name, tag)
		if err != nil {
			return fmt.Errorf("postgres: lock activation status: %w", err)
		}
		objects, err := scanRows(rows)
		if err != nil {
			return err
		}
		if len(objects) == 0 {
			return ErrNotFound
		}
		object := objects[0]
		var current activation.Status
		if document, ok := object.Status["activation"].(map[string]interface{}); ok {
			current, err = activation.DecodeDocument(document)
		} else {
			current, err = activation.NewForMCPServer(object, time.Now().UTC())
		}
		if err != nil {
			return err
		}
		result, err = current.Observe(observation)
		if err != nil {
			return err
		}
		document, err := activation.Document(result)
		if err != nil {
			return err
		}
		encoded, err := json.Marshal(document)
		if err != nil {
			return fmt.Errorf("postgres: marshal activation status: %w", err)
		}
		ct, err := tx.Exec(ctx, `UPDATE registry.artifacts
			SET status=jsonb_set(coalesce(status, '{}'::jsonb), '{activation}', $5::jsonb, true), updated_at=now()
			WHERE kind=$1 AND namespace=$2 AND name=$3 AND tag=$4`,
			string(v1alpha1.KindMCPServer), ns, name, tag, encoded)
		if err != nil {
			return fmt.Errorf("postgres: store activation status: %w", err)
		}
		if ct.RowsAffected() != 1 {
			return ErrNotFound
		}
		return nil
	})
	if err != nil {
		return activation.Status{}, err
	}
	return result, nil
}

func (p *Postgres) SetStatus(ctx context.Context, kind v1alpha1.Kind, ns, name, tag, status string) error {
	q := `UPDATE registry.artifacts SET status = jsonb_set(status, '{status}', $5::jsonb)`
	if status == "deleted" {
		q += `, deletion_timestamp = now()`
	}
	q += ` WHERE kind=$1 AND namespace=$2 AND name=$3 AND tag=$4`
	statusJSON, err := json.Marshal(status)
	if err != nil {
		return fmt.Errorf("postgres: marshal status: %w", err)
	}
	ct, err := p.pool.Exec(ctx, q, string(kind), ns, name, tag, statusJSON)
	if err != nil {
		return err
	}
	if ct.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (p *Postgres) Counts(ctx context.Context, ns string, canRead func(v1alpha1.Object) bool) (map[v1alpha1.Kind]int, error) {
	counts := map[v1alpha1.Kind]int{}
	for _, k := range v1alpha1.AllKinds {
		// Count every readable latest artifact of this kind: List bounds the SQL
		// scan at maxScanRows internally, and we ask the pipeline for the full
		// (bounded) set rather than a single page so the count isn't truncated.
		r, err := p.List(ctx, ListOptions{Kind: k, Namespace: ns, LatestOnly: true, CanRead: canRead, Limit: maxScanRows})
		if err != nil {
			return nil, err
		}
		counts[k] = len(r.Items)
	}
	return counts, nil
}

func (p *Postgres) Health(ctx context.Context) error { return p.pool.Ping(ctx) }
func (p *Postgres) Close() error                     { p.pool.Close(); return nil }

// ---- row scanning ----------------------------------------------------------

const cols = `kind, namespace, name, tag, uid, api_version, visibility, tenant_id,
	org_id, team_id, content_hash, labels, annotations, spec, status,
	created_at, updated_at, deletion_timestamp`

func (p *Postgres) scanOne(ctx context.Context, q string, args ...interface{}) (v1alpha1.Object, error) {
	rows, err := p.pool.Query(ctx, q, args...)
	if err != nil {
		return v1alpha1.Object{}, err
	}
	defer rows.Close()
	objs, err := scanRows(rows)
	if err != nil {
		return v1alpha1.Object{}, err
	}
	if len(objs) == 0 {
		return v1alpha1.Object{}, ErrNotFound
	}
	return objs[0], nil
}

func scanRows(rows pgx.Rows) ([]v1alpha1.Object, error) {
	var out []v1alpha1.Object
	for rows.Next() {
		var (
			o                              v1alpha1.Object
			kind                           string
			orgID, teamID                  *string
			labels, annos, spec, statusRaw []byte
			createdAt, updatedAt           time.Time
			deletedAt                      *time.Time
		)
		err := rows.Scan(
			&kind, &o.Metadata.Namespace, &o.Metadata.Name, &o.Metadata.Tag, &o.Metadata.UID,
			&o.APIVersion, &o.Metadata.Visibility, &o.Metadata.TenantID,
			&orgID, &teamID, &o.Metadata.ContentHash, &labels, &annos, &spec, &statusRaw,
			&createdAt, &updatedAt, &deletedAt,
		)
		if err != nil {
			return nil, err
		}
		o.Kind = v1alpha1.Kind(kind)
		if orgID != nil {
			o.Metadata.OrgID = *orgID
		}
		if teamID != nil {
			o.Metadata.TeamID = *teamID
		}
		// Propagate JSONB unmarshal errors instead of silently dropping the
		// labels/annotations/spec/status — a swallowed error here is silent data
		// loss (an artifact served with an empty spec, etc.). The column type is
		// jsonb so a decode failure means real corruption worth surfacing.
		ref := o.Metadata.Namespace + "/" + o.Metadata.Name
		if err := json.Unmarshal(labels, &o.Metadata.Labels); err != nil {
			return nil, fmt.Errorf("postgres: unmarshal labels for %s: %w", ref, err)
		}
		if err := json.Unmarshal(annos, &o.Metadata.Annotations); err != nil {
			return nil, fmt.Errorf("postgres: unmarshal annotations for %s: %w", ref, err)
		}
		if err := json.Unmarshal(spec, &o.Spec); err != nil {
			return nil, fmt.Errorf("postgres: unmarshal spec for %s: %w", ref, err)
		}
		if err := json.Unmarshal(statusRaw, &o.Status); err != nil {
			return nil, fmt.Errorf("postgres: unmarshal status for %s: %w", ref, err)
		}
		o.Metadata.CreatedAt = &createdAt
		o.Metadata.UpdatedAt = &updatedAt
		o.Metadata.DeletionTimestamp = deletedAt
		out = append(out, o)
	}
	return out, rows.Err()
}

func orEmpty(m map[string]interface{}) map[string]interface{} {
	if m == nil {
		return map[string]interface{}{}
	}
	return m
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

var _ = errors.Is // reserved for future typed-error mapping
