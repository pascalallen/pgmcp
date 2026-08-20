package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// databaseSizesQuery lists the non-template databases by size, largest first.
const databaseSizesQuery = `
SELECT datname, pg_database_size(datname)
FROM pg_database
WHERE NOT datistemplate
ORDER BY 2 DESC`

// overviewCountersQuery reads the cluster-wide cache hit ratio, the number of
// client backends and whether pg_stat_statements is installed.
const overviewCountersQuery = `
SELECT (SELECT sum(blks_hit)::float / GREATEST(sum(blks_hit) + sum(blks_read), 1) FROM pg_stat_database),
       (SELECT count(*) FROM pg_stat_activity WHERE backend_type = 'client backend'),
       EXISTS(SELECT 1 FROM pg_extension WHERE extname = 'pg_stat_statements')`

// Overview summarises the server: identity, database sizes, cache hit ratio,
// connection usage and whether query statistics are available.
func (s *Store) Overview(ctx context.Context) (*diagnostics.Overview, error) {
	overview := &diagnostics.Overview{Databases: make([]diagnostics.DatabaseSize, 0)}

	if err := s.readOnly(ctx, 0, func(ctx context.Context, tx *sql.Tx) error {
		info, err := serverInfo(ctx, tx)
		if err != nil {
			return err
		}
		overview.Server = *info
		overview.MaxConnections = info.MaxConns

		rows, err := tx.QueryContext(ctx, databaseSizesQuery)
		if err != nil {
			return fmt.Errorf("postgres: database sizes: %w", err)
		}
		defer func() { _ = rows.Close() }()

		for rows.Next() {
			var (
				database  diagnostics.DatabaseSize
				sizeBytes sql.NullInt64
			)
			if err := rows.Scan(&database.Name, &sizeBytes); err != nil {
				return fmt.Errorf("postgres: scan database size: %w", err)
			}
			database.SizeBytes = sizeBytes.Int64
			overview.Databases = append(overview.Databases, database)
		}
		if err := rows.Err(); err != nil {
			return fmt.Errorf("postgres: database sizes: %w", err)
		}

		var cacheHitRatio sql.NullFloat64
		if err := tx.QueryRowContext(ctx, overviewCountersQuery).Scan(
			&cacheHitRatio,
			&overview.Connections,
			&overview.PgStatStatements,
		); err != nil {
			return fmt.Errorf("postgres: overview counters: %w", err)
		}
		overview.CacheHitRatio = cacheHitRatio.Float64

		return nil
	}); err != nil {
		return nil, err
	}

	return overview, nil
}
