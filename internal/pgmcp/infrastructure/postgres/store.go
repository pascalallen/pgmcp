// Package postgres is the read-only PostgreSQL adapter behind the
// diagnostics port. Every statement it runs goes through Store.readOnly,
// which wraps the work in a READ ONLY transaction with a statement and lock
// timeout and always rolls back.
package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"

	// lib/pq is registered as the "postgres" driver and its *pq.Error is used
	// to classify read-only violations and statement timeouts.
	_ "github.com/lib/pq"
)

// connMaxLifetime bounds how long a pooled connection is reused, so that
// server-side restarts and failovers are picked up without a restart.
const connMaxLifetime = 30 * time.Minute

var _ diagnostics.Diagnostics = (*Store)(nil)

// Store is the PostgreSQL implementation of the diagnostics port.
type Store struct {
	db             *sql.DB
	log            *slog.Logger
	defaultTimeout time.Duration
	database       string
}

// Open connects to dsn, enforcing the pgmcp application name and a read-only
// default transaction mode, sizes the pool to maxConns and verifies the
// connection. callTimeout is the per-call statement timeout used when a
// caller does not supply one.
func Open(ctx context.Context, dsn string, maxConns int, callTimeout time.Duration, log *slog.Logger) (*Store, error) {
	if log == nil {
		return nil, fmt.Errorf("postgres: logger is required")
	}
	if maxConns < 1 {
		maxConns = 1
	}
	if callTimeout <= 0 {
		callTimeout = 30 * time.Second
	}

	connStr, err := withConnParams(dsn)
	if err != nil {
		return nil, err
	}

	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}

	db.SetMaxOpenConns(maxConns)
	db.SetMaxIdleConns(maxConns)
	db.SetConnMaxLifetime(connMaxLifetime)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	store := &Store{db: db, log: log, defaultTimeout: callTimeout}

	// Read the database name through readOnly like every other statement, so
	// the adapter has no path that runs SQL outside a read-only transaction.
	if err := store.readOnly(ctx, callTimeout, func(ctx context.Context, tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, "SELECT current_database()").Scan(&store.database)
	}); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("postgres: current_database: %w", err)
	}

	return store, nil
}

// Close releases the connection pool.
func (s *Store) Close() error { return s.db.Close() }

// Ping verifies the server is reachable.
func (s *Store) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }

// Database returns the name of the connected database.
func (s *Store) Database() string { return s.database }

// enforcedParams are appended to every DSN: they name the client in
// pg_stat_activity and make every transaction read-only by default, which is
// the second of the three read-only layers.
var enforcedParams = [][2]string{
	{"application_name", "pgmcp"},
	{"default_transaction_read_only", "on"},
}

// errInvalidDSN reports an unparsable connection string. Its message is a
// constant on purpose: url.Parse and url.ParseQuery echo the input they
// failed on, and the input is a DSN carrying a password.
var errInvalidDSN = errors.New("postgres: dsn is not a valid postgres URL")

// withConnParams appends the enforced connection parameters to dsn, which may
// be either a postgres:// URL or a libpq key=value string. Existing
// parameters are preserved; a conflicting application_name or
// default_transaction_read_only is overridden. Errors never quote the dsn.
func withConnParams(dsn string) (string, error) {
	trimmed := strings.TrimSpace(dsn)

	if strings.HasPrefix(trimmed, "postgres://") || strings.HasPrefix(trimmed, "postgresql://") {
		parsed, err := url.Parse(trimmed)
		if err != nil {
			return "", errInvalidDSN
		}
		query, err := url.ParseQuery(parsed.RawQuery)
		if err != nil {
			return "", errInvalidDSN
		}
		for _, param := range enforcedParams {
			query.Set(param[0], param[1])
		}
		parsed.RawQuery = query.Encode()
		return parsed.String(), nil
	}

	// libpq key=value form: later occurrences win, so appending overrides.
	parts := make([]string, 0, len(enforcedParams)+1)
	if trimmed != "" {
		parts = append(parts, trimmed)
	}
	for _, param := range enforcedParams {
		parts = append(parts, param[0]+"="+param[1])
	}

	return strings.Join(parts, " "), nil
}
