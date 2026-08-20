package postgres

import (
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// testDSN returns the DSN integration tests run against, skipping the test
// when PGMCP_TEST_DSN is unset so that a plain `go test ./...` needs no
// database.
func testDSN(t *testing.T) string {
	t.Helper()

	dsn := os.Getenv("PGMCP_TEST_DSN")
	if dsn == "" {
		t.Skip("PGMCP_TEST_DSN is not set; skipping integration test")
	}

	return dsn
}

// testStore opens a Store against PGMCP_TEST_DSN and closes it on cleanup.
func testStore(t *testing.T) *Store {
	t.Helper()

	dsn := testDSN(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := Open(ctx, dsn, 4, 5*time.Second, testLogger())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, store.Close()) })

	return store
}

// testLogger returns a logger that discards everything.
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// rawDB opens a pool straight on PGMCP_TEST_DSN, bypassing the Store: tests
// need a connection that may write and hold locks, which the Store's
// read-only connections cannot.
func rawDB(t *testing.T) *sql.DB {
	t.Helper()

	db, err := sql.Open("postgres", testDSN(t))
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })

	return db
}

// rawConn pins a single writable connection, so that BEGIN, LOCK TABLE and
// the session's backend pid all belong to the same backend.
func rawConn(t *testing.T, ctx context.Context) *sql.Conn {
	t.Helper()

	conn, err := rawDB(t).Conn(ctx)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	return conn
}

// backendPID returns the server-side process id of conn.
func backendPID(t *testing.T, ctx context.Context, conn *sql.Conn) int {
	t.Helper()

	var pid int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT pg_backend_pid()").Scan(&pid))

	return pid
}

// seedLockFixture creates the table the lock-wait test blocks on. It is
// idempotent so repeated runs against the same database are safe.
func seedLockFixture(t *testing.T, ctx context.Context) {
	t.Helper()

	db := rawDB(t)
	_, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS pgmcp_test")
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS pgmcp_test.lw_t(a int)")
	require.NoError(t, err)
}
