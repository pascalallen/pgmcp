package postgres

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// serverInfoQuery reads the identity, uptime and role of the server.
const serverInfoQuery = `
SELECT current_setting('server_version'),
       current_setting('server_version_num')::int,
       current_database(),
       EXTRACT(EPOCH FROM now() - pg_postmaster_start_time()),
       pg_is_in_recovery(),
       current_setting('max_connections')::int`

// extensionsQuery lists the installed extensions.
const extensionsQuery = `SELECT extname FROM pg_extension ORDER BY 1`

// ServerInfo reports the connected server's version, uptime, recovery state,
// connection ceiling and installed extensions.
func (s *Store) ServerInfo(ctx context.Context) (*diagnostics.ServerInfo, error) {
	var info *diagnostics.ServerInfo

	if err := s.readOnly(ctx, 0, func(ctx context.Context, tx *sql.Tx) error {
		var err error
		info, err = serverInfo(ctx, tx)
		return err
	}); err != nil {
		return nil, err
	}

	return info, nil
}

// serverInfo reads the server identity inside an existing read-only
// transaction, so that callers needing it alongside other reads (Overview,
// Replication) pay for a single transaction.
func serverInfo(ctx context.Context, tx *sql.Tx) (*diagnostics.ServerInfo, error) {
	info := &diagnostics.ServerInfo{Extensions: make([]string, 0)}

	if err := tx.QueryRowContext(ctx, serverInfoQuery).Scan(
		&info.Version,
		&info.VersionNum,
		&info.Database,
		&info.UptimeSec,
		&info.InRecovery,
		&info.MaxConns,
	); err != nil {
		return nil, fmt.Errorf("postgres: server info: %w", err)
	}

	rows, err := tx.QueryContext(ctx, extensionsQuery)
	if err != nil {
		return nil, fmt.Errorf("postgres: extensions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, fmt.Errorf("postgres: scan extension: %w", err)
		}
		info.Extensions = append(info.Extensions, name)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("postgres: extensions: %w", err)
	}

	return info, nil
}
