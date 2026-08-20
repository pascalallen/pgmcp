package postgres

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreReadOnly(t *testing.T) {
	t.Run("a write inside the transaction fails with a read-only violation", func(t *testing.T) {
		store := testStore(t)

		err := store.readOnly(context.Background(), time.Second, func(ctx context.Context, tx *sql.Tx) error {
			_, execErr := tx.ExecContext(ctx, "CREATE TEMP TABLE pgmcp_write_probe(a int)")
			return execErr
		})

		var violation *diagnostics.ReadOnlyViolation
		require.ErrorAs(t, err, &violation)
		assert.NotEmpty(t, violation.Msg)
	})

	t.Run("the statement timeout is enforced", func(t *testing.T) {
		store := testStore(t)

		start := time.Now()
		err := store.readOnly(context.Background(), 200*time.Millisecond, func(ctx context.Context, tx *sql.Tx) error {
			_, execErr := tx.ExecContext(ctx, "SELECT pg_sleep(2)")
			return execErr
		})
		elapsed := time.Since(start)

		require.Error(t, err)
		assert.Contains(t, err.Error(), "statement timeout")
		assert.True(t, errors.Is(err, context.DeadlineExceeded), "expected a DeadlineExceeded chain, got %v", err)
		assert.Less(t, elapsed, 1500*time.Millisecond, "expected the statement to be cancelled early, took %s", elapsed)
	})

	t.Run("a rollback leaves no open transaction on the connection", func(t *testing.T) {
		store := testStore(t)
		store.db.SetMaxOpenConns(1)

		require.NoError(t, store.readOnly(context.Background(), time.Second, func(ctx context.Context, tx *sql.Tx) error {
			var one int
			return tx.QueryRowContext(ctx, "SELECT 1").Scan(&one)
		}))

		var state string
		require.NoError(t, store.readOnly(context.Background(), time.Second, func(ctx context.Context, tx *sql.Tx) error {
			return tx.QueryRowContext(ctx, "SELECT state FROM pg_stat_activity WHERE pid = pg_backend_pid()").Scan(&state)
		}))
		assert.Equal(t, "active", state)
	})

	t.Run("the callers error is returned unwrapped", func(t *testing.T) {
		store := testStore(t)
		sentinel := errors.New("boom")

		err := store.readOnly(context.Background(), time.Second, func(context.Context, *sql.Tx) error {
			return sentinel
		})

		require.ErrorIs(t, err, sentinel)
	})

	t.Run("a zero timeout falls back to the stores default", func(t *testing.T) {
		store := testStore(t)
		store.defaultTimeout = 200 * time.Millisecond

		start := time.Now()
		err := store.readOnly(context.Background(), 0, func(ctx context.Context, tx *sql.Tx) error {
			_, execErr := tx.ExecContext(ctx, "SELECT pg_sleep(2)")
			return execErr
		})

		require.Error(t, err)
		assert.Contains(t, err.Error(), "statement timeout")
		assert.Less(t, time.Since(start), 1500*time.Millisecond)
	})
}

func TestOpen(t *testing.T) {
	t.Run("the store records the connected database and answers pings", func(t *testing.T) {
		store := testStore(t)

		require.NoError(t, store.Ping(context.Background()))
		assert.NotEmpty(t, store.database)
	})

	t.Run("an unreachable server fails to open", func(t *testing.T) {
		testDSN(t)

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		_, err := Open(ctx, "postgres://postgres:pg@127.0.0.1:1/postgres?sslmode=disable&connect_timeout=1", 2, time.Second, testLogger())

		require.Error(t, err)
	})
}
