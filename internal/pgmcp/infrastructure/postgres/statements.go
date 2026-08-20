package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// topQueriesOrderExprs maps an ordering onto the column it sorts by. Only
// these fixed expressions are ever interpolated into the statement query —
// the caller's value selects one, it is never used as SQL itself.
var topQueriesOrderExprs = map[diagnostics.OrderBy]string{
	diagnostics.OrderByTotalTime:      "s.total_exec_time",
	diagnostics.OrderByMeanTime:       "s.mean_exec_time",
	diagnostics.OrderByCalls:          "s.calls",
	diagnostics.OrderByRows:           "s.rows",
	diagnostics.OrderBySharedBlksRead: "s.shared_blks_read",
}

// defaultTopQueriesOrderBy is used when the caller does not choose one.
const defaultTopQueriesOrderBy = diagnostics.OrderByTotalTime

// Bounds on how many statements a single call reports.
const (
	defaultTopQueriesLimit = 20
	maxTopQueriesLimit     = 100
)

// pgStatStatementsHint tells the operator how to install the extension the
// query statistics come from.
const pgStatStatementsHint = "Run `CREATE EXTENSION pg_stat_statements;` " +
	"(requires shared_preload_libraries=pg_stat_statements) and restart."

// statsSinceMinVersion is the first server_version_num that has the
// pg_stat_statements_info view (PostgreSQL 14); on 13 the reset timestamp is
// simply unknown.
const statsSinceMinVersion = 140000

// pgStatStatementsProbeQuery reports whether the extension is installed and
// which server version is answering, which decides whether the reset
// timestamp can be read.
const pgStatStatementsProbeQuery = `
SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements'),
       current_setting('server_version_num')::int`

// statsSinceQuery reads when the statistics were last reset.
const statsSinceQuery = `SELECT stats_reset FROM pg_stat_statements_info`

// topQueriesQuery lists the heaviest statements. The single %s is filled from
// topQueriesOrderExprs; every value the caller supplies is bound. Column
// names are the PostgreSQL 13+ spelling (total_exec_time and friends), and
// left() truncates the recorded text to 2000 characters.
const topQueriesQuery = `
SELECT s.queryid,
       left(s.query, 2000),
       s.calls,
       s.total_exec_time,
       s.mean_exec_time,
       s.stddev_exec_time,
       s.rows,
       CASE WHEN s.shared_blks_hit + s.shared_blks_read = 0 THEN 1
            ELSE s.shared_blks_hit::float / (s.shared_blks_hit + s.shared_blks_read) END,
       s.temp_blks_read + s.temp_blks_written
FROM pg_stat_statements s
JOIN pg_database d ON d.oid = s.dbid
WHERE ($1 = '' OR d.datname = $1)
  AND s.calls >= $2
  AND s.queryid IS NOT NULL
ORDER BY %s DESC NULLS LAST
LIMIT $3`

// TopQueries reports the heaviest statements pg_stat_statements has recorded,
// ordered by the requested column. A missing extension is not an error: the
// result reports it as unavailable and carries the hint that installs it.
func (s *Store) TopQueries(ctx context.Context, p diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
	orderBy := p.OrderBy
	if orderBy == "" {
		orderBy = defaultTopQueriesOrderBy
	}
	orderExpr, ok := topQueriesOrderExprs[orderBy]
	if !ok {
		return nil, fmt.Errorf("postgres: unsupported top queries ordering %q", orderBy)
	}

	limit := p.Limit
	switch {
	case limit <= 0:
		limit = defaultTopQueriesLimit
	case limit > maxTopQueriesLimit:
		limit = maxTopQueriesLimit
	}

	minCalls := p.MinCalls
	if minCalls < 0 {
		minCalls = 0
	}

	result := &diagnostics.TopQueriesResult{Statements: make([]diagnostics.StatementStat, 0, limit)}

	if err := s.readOnly(ctx, 0, func(ctx context.Context, tx *sql.Tx) error {
		var (
			installed  bool
			versionNum int
		)
		if err := tx.QueryRowContext(ctx, pgStatStatementsProbeQuery).Scan(&installed, &versionNum); err != nil {
			return fmt.Errorf("postgres: pg_stat_statements probe: %w", err)
		}
		if !installed {
			result.Hint = pgStatStatementsHint

			return nil
		}
		result.Available = true

		if versionNum >= statsSinceMinVersion {
			var statsReset sql.NullTime
			if err := tx.QueryRowContext(ctx, statsSinceQuery).Scan(&statsReset); err != nil {
				return fmt.Errorf("postgres: pg_stat_statements_info: %w", err)
			}
			if statsReset.Valid {
				resetAt := statsReset.Time
				result.StatsSince = &resetAt
			}
		}

		rows, err := tx.QueryContext(ctx, fmt.Sprintf(topQueriesQuery, orderExpr), p.Database, minCalls, limit)
		if err != nil {
			return fmt.Errorf("postgres: top queries: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var (
				statement diagnostics.StatementStat
				// The recorded text is NULL for a statement the current role
				// may not see, and the ratio is NULL when the counters are.
				query    sql.NullString
				hitRatio sql.NullFloat64
			)
			if err := rows.Scan(
				&statement.QueryID,
				&query,
				&statement.Calls,
				&statement.TotalMs,
				&statement.MeanMs,
				&statement.StddevMs,
				&statement.Rows,
				&hitRatio,
				&statement.TempBlks,
			); err != nil {
				return fmt.Errorf("postgres: scan statement: %w", err)
			}
			statement.Query = query.String
			statement.HitRatio = hitRatio.Float64
			result.Statements = append(result.Statements, statement)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("postgres: top queries: %w", err)
		}

		return nil
	}); err != nil {
		return nil, err
	}

	return result, nil
}
