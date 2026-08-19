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

// seedFixtures creates the schema the index and table health tests inspect: a
// table with two identical indexes on the same column (one unused, one a
// duplicate of the other) and half its rows deleted but not vacuumed, so it
// carries dead tuples and index bloat. Autovacuum is disabled on the table so
// that the dead tuples survive between runs. It is idempotent: the rows are
// only written when the table is empty. No test may scan pgmcp_test.bloaty(pad):
// doing so would move bloaty_pad_idx out of the unused listing for good.
func seedFixtures(t *testing.T, db *sql.DB) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	for _, stmt := range []string{
		"CREATE SCHEMA IF NOT EXISTS pgmcp_test",
		"CREATE TABLE IF NOT EXISTS pgmcp_test.bloaty(id serial primary key, pad text)",
		"ALTER TABLE pgmcp_test.bloaty SET (autovacuum_enabled = false)",
		"CREATE INDEX IF NOT EXISTS bloaty_pad_idx ON pgmcp_test.bloaty(pad)",
		"CREATE INDEX IF NOT EXISTS bloaty_pad_idx2 ON pgmcp_test.bloaty(pad)",
		"CREATE TABLE IF NOT EXISTS pgmcp_test.lw_t(a int)",
		"CREATE TABLE IF NOT EXISTS pgmcp_test.ro_probe(id serial primary key, val text)",
	} {
		_, err := db.ExecContext(ctx, stmt)
		require.NoError(t, err)
	}

	var probeRows int64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM pgmcp_test.ro_probe").Scan(&probeRows))
	if probeRows == 0 {
		_, err := db.ExecContext(ctx,
			"INSERT INTO pgmcp_test.ro_probe(val) SELECT 'original' FROM generate_series(1, 3)")
		require.NoError(t, err)
	}

	var rows int64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM pgmcp_test.bloaty").Scan(&rows))
	if rows == 0 {
		// Every pad is distinct: identical values would be collapsed by btree
		// deduplication into an index far too small to bloat measurably.
		_, err := db.ExecContext(ctx,
			"INSERT INTO pgmcp_test.bloaty(pad) SELECT repeat('a', 100) || g FROM generate_series(1, 20000) g")
		require.NoError(t, err)

		// Deleting without vacuuming leaves the dead tuples and the index
		// entries pointing at them in place, which is the condition under test.
		_, err = db.ExecContext(ctx, "DELETE FROM pgmcp_test.bloaty WHERE id % 2 = 0")
		require.NoError(t, err)
	}

	// ANALYZE (never VACUUM) refreshes reltuples and the column statistics the
	// bloat estimation queries read, without removing a single dead tuple.
	_, err := db.ExecContext(ctx, "ANALYZE pgmcp_test.bloaty")
	require.NoError(t, err)
}
