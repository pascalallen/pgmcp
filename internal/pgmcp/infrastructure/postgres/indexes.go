package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/lib/pq"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// unusedIndexesQuery lists secondary indexes the planner has never chosen.
// Unique and primary key indexes are excluded because they enforce a
// constraint whatever their scan count, and invalid indexes are reported
// separately.
const unusedIndexesQuery = `
SELECT s.schemaname,
       s.relname,
       s.indexrelname,
       pg_relation_size(s.indexrelid),
       s.idx_scan,
       pg_get_indexdef(s.indexrelid)
FROM pg_stat_user_indexes s
JOIN pg_index i USING (indexrelid)
WHERE NOT i.indisunique
  AND NOT i.indisprimary
  AND i.indisvalid
  AND s.idx_scan = 0
  AND ($1 = '' OR s.schemaname = $1)
ORDER BY 4 DESC
LIMIT 100`

// duplicateIndexesQuery pairs indexes on the same table that index the same
// columns with the same operator classes, the same partial predicate and the
// same expressions — that is, indexes one of which is redundant. The
// indexrelid comparison keeps one row per pair and makes the older index the
// one to keep.
const duplicateIndexesQuery = `
SELECT n.nspname,
       c.relname,
       a.relname AS index,
       b.relname AS duplicate_of,
       pg_relation_size(a.oid),
       pg_get_indexdef(a.oid)
FROM pg_index x
JOIN pg_index y ON x.indrelid = y.indrelid
                AND x.indexrelid <> y.indexrelid
                AND x.indkey::text = y.indkey::text
                AND x.indclass::text = y.indclass::text
                AND coalesce(pg_get_expr(x.indpred, x.indrelid), '') = coalesce(pg_get_expr(y.indpred, y.indrelid), '')
                AND coalesce(pg_get_expr(x.indexprs, x.indrelid), '') = coalesce(pg_get_expr(y.indexprs, y.indrelid), '')
                AND x.indexrelid > y.indexrelid
JOIN pg_class a ON a.oid = x.indexrelid
JOIN pg_class b ON b.oid = y.indexrelid
JOIN pg_class c ON c.oid = x.indrelid
JOIN pg_namespace n ON n.oid = c.relnamespace
WHERE n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND ($1 = '' OR n.nspname = $1)
ORDER BY 5 DESC
LIMIT 100`

// invalidIndexesQuery lists indexes the planner refuses to use: a failed
// CREATE INDEX CONCURRENTLY or REINDEX CONCURRENTLY leaves one behind, still
// costing writes and disk while serving nothing.
const invalidIndexesQuery = `
SELECT n.nspname,
       t.relname,
       c.relname,
       pg_relation_size(c.oid),
       pg_get_indexdef(c.oid)
FROM pg_index i
JOIN pg_class c ON c.oid = i.indexrelid
JOIN pg_class t ON t.oid = i.indrelid
JOIN pg_namespace n ON n.oid = t.relnamespace
WHERE NOT i.indisvalid
  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
  AND ($1 = '' OR n.nspname = $1)
ORDER BY 4 DESC
LIMIT 100`

// IndexHealth reports indexes that are never scanned, redundant with another
// index, invalid, or — when IncludeBloat is set — estimated to be badly
// bloated. Nothing is dropped: the DropCandidateSQL on an unused finding is
// text for a human to review.
func (s *Store) IndexHealth(ctx context.Context, p diagnostics.IndexHealthParams) (*diagnostics.IndexHealthResult, error) {
	result := &diagnostics.IndexHealthResult{
		Unused:    make([]diagnostics.IndexFinding, 0),
		Duplicate: make([]diagnostics.IndexFinding, 0),
		Invalid:   make([]diagnostics.IndexFinding, 0),
		Bloated:   make([]diagnostics.IndexFinding, 0),
	}

	if err := s.readOnly(ctx, 0, func(ctx context.Context, tx *sql.Tx) error {
		unused, err := scanIndexFindings(ctx, tx, unusedIndexesQuery, p.Schema)
		if err != nil {
			return fmt.Errorf("postgres: unused indexes: %w", err)
		}
		for i := range unused {
			unused[i].DropCandidateSQL = dropIndexSQL(unused[i].Schema, unused[i].Index)
		}
		result.Unused = unused

		duplicate, err := scanDuplicateFindings(ctx, tx, p.Schema)
		if err != nil {
			return fmt.Errorf("postgres: duplicate indexes: %w", err)
		}
		result.Duplicate = duplicate

		invalid, err := scanIndexFindings(ctx, tx, invalidIndexesQuery, p.Schema)
		if err != nil {
			return fmt.Errorf("postgres: invalid indexes: %w", err)
		}
		result.Invalid = invalid

		if !p.IncludeBloat {
			return nil
		}

		bloated, err := scanBloatFindings(ctx, tx, indexBloatQuery, p.Schema)
		if err != nil {
			return fmt.Errorf("postgres: index bloat: %w", err)
		}
		result.Bloated = bloated

		return nil
	}); err != nil {
		return nil, err
	}

	return result, nil
}

// scanIndexFindings runs one of the index catalogue queries whose rows are
// (schema, table, index, size, scans, definition).
func scanIndexFindings(ctx context.Context, tx *sql.Tx, query, schema string) ([]diagnostics.IndexFinding, error) {
	rows, err := tx.QueryContext(ctx, query, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	findings := make([]diagnostics.IndexFinding, 0)
	for rows.Next() {
		var (
			finding    diagnostics.IndexFinding
			size       sql.NullInt64
			scans      sql.NullInt64
			definition sql.NullString
		)
		if err := rows.Scan(&finding.Schema, &finding.Table, &finding.Index, &size, &scans, &definition); err != nil {
			return nil, err
		}

		finding.SizeBytes = size.Int64
		finding.Scans = scans.Int64
		finding.Definition = definition.String

		findings = append(findings, finding)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return findings, nil
}

// scanDuplicateFindings runs the duplicate query, whose rows name the index
// that is duplicated instead of a scan count.
func scanDuplicateFindings(ctx context.Context, tx *sql.Tx, schema string) ([]diagnostics.IndexFinding, error) {
	rows, err := tx.QueryContext(ctx, duplicateIndexesQuery, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	findings := make([]diagnostics.IndexFinding, 0)
	for rows.Next() {
		var (
			finding     diagnostics.IndexFinding
			duplicateOf sql.NullString
			size        sql.NullInt64
			definition  sql.NullString
		)
		if err := rows.Scan(
			&finding.Schema, &finding.Table, &finding.Index, &duplicateOf, &size, &definition,
		); err != nil {
			return nil, err
		}

		finding.DuplicateOf = duplicateOf.String
		finding.SizeBytes = size.Int64
		finding.Definition = definition.String

		findings = append(findings, finding)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return findings, nil
}

// scanBloatFindings runs a bloat estimation query, whose rows are
// (schema, table, index, real_size, bloat_size, bloat_pct).
func scanBloatFindings(ctx context.Context, tx *sql.Tx, query, schema string) ([]diagnostics.IndexFinding, error) {
	rows, err := tx.QueryContext(ctx, query, schema)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	findings := make([]diagnostics.IndexFinding, 0)
	for rows.Next() {
		var (
			finding    diagnostics.IndexFinding
			realSize   sql.NullInt64
			bloatSize  sql.NullInt64
			bloatPct   sql.NullFloat64
			indexName  sql.NullString
			tableName  sql.NullString
			schemaName sql.NullString
		)
		if err := rows.Scan(&schemaName, &tableName, &indexName, &realSize, &bloatSize, &bloatPct); err != nil {
			return nil, err
		}

		finding.Schema = schemaName.String
		finding.Table = tableName.String
		finding.Index = indexName.String
		finding.SizeBytes = realSize.Int64
		finding.BloatBytes = bloatSize.Int64
		finding.BloatRatio = bloatPct.Float64 / 100

		findings = append(findings, finding)
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	return findings, nil
}

// dropIndexSQL renders the statement that would drop an index. It is only ever
// returned as text — this package never executes DDL — and both identifiers
// are quoted so the caller sees a statement that is safe to paste.
func dropIndexSQL(schema, index string) string {
	return fmt.Sprintf("DROP INDEX CONCURRENTLY %s.%s;", pq.QuoteIdentifier(schema), pq.QuoteIdentifier(index))
}
