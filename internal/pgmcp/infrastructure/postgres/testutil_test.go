package postgres

import (
	"context"
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
