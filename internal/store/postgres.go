package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

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
`

// Postgres is the production Store backed by PostgreSQL. spec/status/labels are
// JSONB; identity and scope are promoted to real columns and GIN-indexed.
type Postgres struct {
	pool *pgxpool.Pool
}

// NewPostgres connects to dsn, optionally applies the embedded schema, and
// returns a ready Store.
func NewPostgres(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: connect: %w", err)
	}
	if os.Getenv("AUTO_MIGRATE") != "false" {
		if _, err := pool.Exec(ctx, schemaSQL); err != nil {
			pool.Close()
			return nil, fmt.Errorf("postgres: migrate: %w", err)
		}
	}
	return &Postgres{pool: pool}, nil
}

func (p *Postgres) Apply(ctx context.Context, obj v1alpha1.Object) (v1alpha1.Object, bool, error) {
	obj = obj.Normalized()
	hash := obj.ContentHash()
	labels, _ := json.Marshal(obj.Metadata.Labels)
	annos, _ := json.Marshal(obj.Metadata.Annotations)
	spec, _ := json.Marshal(orEmpty(obj.Spec))
	status := map[string]interface{}{"status": "active"}
	statusJSON, _ := json.Marshal(status)
	uid := uuid.NewString()

	const q = `
INSERT INTO registry.artifacts
    (kind, namespace, name, tag, uid, api_version, visibility, tenant_id, org_id, team_id,
     content_hash, labels, annotations, spec, status, created_at, updated_at, deletion_timestamp)
VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15, now(), now(), NULL)
ON CONFLICT (kind, namespace, name, tag) DO UPDATE SET
    api_version  = EXCLUDED.api_version,
    visibility   = EXCLUDED.visibility,
    tenant_id    = EXCLUDED.tenant_id,
    org_id       = EXCLUDED.org_id,
    team_id      = EXCLUDED.team_id,
    content_hash = EXCLUDED.content_hash,
    labels       = EXCLUDED.labels,
    annotations  = EXCLUDED.annotations,
    spec         = EXCLUDED.spec,
    status       = registry.artifacts.status,
    updated_at   = now(),
    deletion_timestamp = NULL
RETURNING (xmax = 0) AS inserted, uid, created_at, updated_at`

	var inserted bool
	var gotUID string
	var createdAt, updatedAt time.Time
	err := p.pool.QueryRow(ctx, q,
		string(obj.Kind), obj.Metadata.Namespace, obj.Metadata.Name, obj.Metadata.Tag, uid,
		obj.APIVersion, string(obj.Metadata.Visibility), obj.Metadata.TenantID,
		nullStr(obj.Metadata.OrgID), nullStr(obj.Metadata.TeamID),
		hash, labels, annos, spec, statusJSON,
	).Scan(&inserted, &gotUID, &createdAt, &updatedAt)
	if err != nil {
		return v1alpha1.Object{}, false, fmt.Errorf("postgres: apply: %w", err)
	}

	obj.Metadata.UID = gotUID
	obj.Metadata.ContentHash = hash
	obj.Metadata.CreatedAt = &createdAt
	obj.Metadata.UpdatedAt = &updatedAt
	obj.Status = status
	return obj, inserted, nil
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

func (p *Postgres) List(ctx context.Context, opts ListOptions) (ListResult, error) {
	// v1 strategy: fetch the candidate set by kind/namespace/deletion in SQL,
	// then run the shared discovery pipeline (visibility/RBAC pre-filter →
	// selector → search → collapse → paginate) in Go for identical semantics
	// across backends. GIN-pushdown of the selector is a v2 optimization.
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
	q := "SELECT " + cols + " FROM registry.artifacts WHERE " + where
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

func (p *Postgres) SetStatus(ctx context.Context, kind v1alpha1.Kind, ns, name, tag, status string) error {
	q := `UPDATE registry.artifacts SET status = jsonb_set(status, '{status}', $5::jsonb)`
	if status == "deleted" {
		q += `, deletion_timestamp = now()`
	}
	q += ` WHERE kind=$1 AND namespace=$2 AND name=$3 AND tag=$4`
	statusJSON, _ := json.Marshal(status)
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
		r, err := p.List(ctx, ListOptions{Kind: k, Namespace: ns, LatestOnly: true, CanRead: canRead, Limit: 1 << 30})
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
		_ = json.Unmarshal(labels, &o.Metadata.Labels)
		_ = json.Unmarshal(annos, &o.Metadata.Annotations)
		_ = json.Unmarshal(spec, &o.Spec)
		_ = json.Unmarshal(statusRaw, &o.Status)
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
