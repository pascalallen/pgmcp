package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// lockWaitsQuery reports one row per blocked/blocking pair, joining the
// waiting backend to each backend pg_blocking_pids names and to the lock it
// is waiting on.
const lockWaitsQuery = `
SELECT w.pid,
       left(w.query, 500),
       w.usename,
       b.pid,
       left(b.query, 500),
       b.usename,
       l.locktype,
       l.mode,
       coalesce(l.relation::regclass::text, ''),
       EXTRACT(EPOCH FROM now() - w.state_change) * 1000
FROM pg_stat_activity w
JOIN LATERAL unnest(pg_blocking_pids(w.pid)) AS bp(pid) ON true
JOIN pg_stat_activity b ON b.pid = bp.pid
JOIN pg_locks l ON l.pid = w.pid AND NOT l.granted
WHERE cardinality(pg_blocking_pids(w.pid)) > 0
  AND EXTRACT(EPOCH FROM now() - w.state_change) * 1000 >= $1
ORDER BY 10 DESC`

// LockWaits reports the sessions currently blocked on a lock, who is blocking
// them, and any deadlock cycles among them.
func (s *Store) LockWaits(ctx context.Context, p diagnostics.LockWaitsParams) (*diagnostics.LockWaitsResult, error) {
	minWaitMs := p.MinWaitMs
	if minWaitMs < 0 {
		minWaitMs = 0
	}

	result := &diagnostics.LockWaitsResult{
		Edges:  make([]diagnostics.LockEdge, 0),
		Cycles: make([][]int, 0),
	}

	if err := s.readOnly(ctx, 0, func(ctx context.Context, tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, lockWaitsQuery, minWaitMs)
		if err != nil {
			return fmt.Errorf("postgres: lock waits: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var (
				edge          diagnostics.LockEdge
				blockedQuery  sql.NullString
				blockedUser   sql.NullString
				blockingQuery sql.NullString
				blockingUser  sql.NullString
				lockType      sql.NullString
				mode          sql.NullString
				relation      sql.NullString
				waitMs        sql.NullFloat64
			)
			if err := rows.Scan(
				&edge.BlockedPID, &blockedQuery, &blockedUser,
				&edge.BlockingPID, &blockingQuery, &blockingUser,
				&lockType, &mode, &relation, &waitMs,
			); err != nil {
				return fmt.Errorf("postgres: scan lock wait: %w", err)
			}

			edge.BlockedQuery = blockedQuery.String
			edge.BlockedUser = blockedUser.String
			edge.BlockingQuery = blockingQuery.String
			edge.BlockingUser = blockingUser.String
			edge.LockType = lockType.String
			edge.Mode = mode.String
			edge.Relation = relation.String
			edge.WaitMs = int64(waitMs.Float64)

			result.Edges = append(result.Edges, edge)
		}

		return rows.Err()
	}); err != nil {
		return nil, err
	}

	if cycles := diagnostics.FindCycles(result.Edges); cycles != nil {
		result.Cycles = cycles
	}

	return result, nil
}
