package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// connectionGroupExprs maps a grouping dimension onto the SQL expression that
// produces its key. Only these fixed expressions are ever interpolated into
// the grouping query — the caller's value selects one, it is never used as
// SQL itself.
var connectionGroupExprs = map[diagnostics.GroupBy]string{
	"state":       "coalesce(state,'<none>')",
	"wait_event":  "coalesce(wait_event_type||':'||wait_event,'<none>')",
	"application": "coalesce(nullif(application_name,''),'<none>')",
	"user":        "coalesce(usename,'<none>')",
	"database":    "coalesce(datname,'<none>')",
}

// defaultConnectionGroupBy is used when the caller does not choose one.
const defaultConnectionGroupBy = diagnostics.GroupBy("state")

// connectionTotalsQuery counts client backends and reads the ceiling they
// compete for.
const connectionTotalsQuery = `
SELECT count(*), current_setting('max_connections')::int
FROM pg_stat_activity
WHERE backend_type = 'client backend'`

// idleInTransactionQuery lists the sessions holding an idle transaction open
// for longer than the caller's threshold.
const idleInTransactionQuery = `
SELECT pid,
       EXTRACT(EPOCH FROM now() - state_change),
       usename,
       application_name,
       left(query, 500)
FROM pg_stat_activity
WHERE state = 'idle in transaction'
  AND now() - state_change > make_interval(secs => $1)
ORDER BY 2 DESC
LIMIT 50`

// maxIdleInTransaction bounds the idle-in-transaction listing, matching the
// LIMIT in idleInTransactionQuery.
const maxIdleInTransaction = 50

// Connections reports how client backends are distributed across the chosen
// dimension, how much of the connection ceiling is used, and which sessions
// are sitting idle inside a transaction.
func (s *Store) Connections(ctx context.Context, p diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error) {
	groupBy := p.GroupBy
	if groupBy == "" {
		groupBy = defaultConnectionGroupBy
	}
	groupExpr, ok := connectionGroupExprs[groupBy]
	if !ok {
		return nil, fmt.Errorf("postgres: unsupported connections grouping %q", string(p.GroupBy))
	}

	idleMinSec := p.IdleInTxMinSec
	if idleMinSec < 0 {
		idleMinSec = 0
	}

	result := &diagnostics.ConnectionsResult{
		Groups:            make([]diagnostics.ConnGroup, 0),
		IdleInTransaction: make([]diagnostics.IdleInTx, 0, maxIdleInTransaction),
	}

	if err := s.readOnly(ctx, 0, func(ctx context.Context, tx *sql.Tx) error {
		groupQuery := fmt.Sprintf(
			"SELECT %s, count(*) FROM pg_stat_activity WHERE backend_type = 'client backend' GROUP BY 1 ORDER BY 2 DESC",
			groupExpr,
		)
		rows, err := tx.QueryContext(ctx, groupQuery)
		if err != nil {
			return fmt.Errorf("postgres: connection groups: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var group diagnostics.ConnGroup
			var key sql.NullString
			if err := rows.Scan(&key, &group.Count); err != nil {
				return fmt.Errorf("postgres: scan connection group: %w", err)
			}
			group.Key = key.String
			result.Groups = append(result.Groups, group)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("postgres: connection groups: %w", err)
		}

		if err := tx.QueryRowContext(ctx, connectionTotalsQuery).Scan(&result.Total, &result.MaxConnections); err != nil {
			return fmt.Errorf("postgres: connection totals: %w", err)
		}

		idleRows, err := tx.QueryContext(ctx, idleInTransactionQuery, idleMinSec)
		if err != nil {
			return fmt.Errorf("postgres: idle in transaction: %w", err)
		}
		defer func() { _ = idleRows.Close() }()

		for idleRows.Next() {
			var (
				idle        diagnostics.IdleInTx
				ageSec      sql.NullFloat64
				user        sql.NullString
				application sql.NullString
				query       sql.NullString
			)
			if err := idleRows.Scan(&idle.PID, &ageSec, &user, &application, &query); err != nil {
				return fmt.Errorf("postgres: scan idle in transaction: %w", err)
			}
			idle.AgeSec = ageSec.Float64
			idle.User = user.String
			idle.Application = application.String
			idle.Query = query.String
			result.IdleInTransaction = append(result.IdleInTransaction, idle)
		}

		return idleRows.Err()
	}); err != nil {
		return nil, err
	}

	if result.MaxConnections > 0 {
		result.UsedPct = float64(result.Total) / float64(result.MaxConnections) * 100
	}

	return result, nil
}
