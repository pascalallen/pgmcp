package postgres

import (
	"context"
	"testing"
	"time"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStoreServerInfo(t *testing.T) {
	t.Run("server info reports the version and the connected database", func(t *testing.T) {
		store := testStore(t)

		info, err := store.ServerInfo(context.Background())

		require.NoError(t, err)
		assert.NotEmpty(t, info.Version)
		assert.GreaterOrEqual(t, info.VersionNum, 130000)
		assert.Equal(t, store.database, info.Database)
		assert.Greater(t, info.UptimeSec, float64(0))
		assert.False(t, info.InRecovery)
		assert.Greater(t, info.MaxConns, 0)
		assert.NotNil(t, info.Extensions)
		assert.Contains(t, info.Extensions, "plpgsql")
	})
}

func TestStoreConnections(t *testing.T) {
	t.Run("connections groups by state and reports the connection ceiling", func(t *testing.T) {
		store := testStore(t)

		result, err := store.Connections(context.Background(), diagnostics.ConnectionsParams{GroupBy: "state"})

		require.NoError(t, err)
		require.NotEmpty(t, result.Groups)
		assert.NotNil(t, result.IdleInTransaction)
		assert.Greater(t, result.Total, 0)
		assert.Greater(t, result.MaxConnections, 0)
		assert.Greater(t, result.UsedPct, float64(0))

		keys := make([]string, 0, len(result.Groups))
		for _, group := range result.Groups {
			keys = append(keys, group.Key)
			assert.Greater(t, group.Count, 0)
		}
		assert.Contains(t, keys, "active")
	})

	t.Run("connections groups by every supported dimension", func(t *testing.T) {
		store := testStore(t)

		for _, groupBy := range []diagnostics.GroupBy{"", "state", "wait_event", "application", "user", "database"} {
			result, err := store.Connections(context.Background(), diagnostics.ConnectionsParams{GroupBy: groupBy})

			require.NoErrorf(t, err, "group by %q", groupBy)
			assert.NotNilf(t, result.Groups, "group by %q", groupBy)
		}
	})

	t.Run("connections rejects an unknown grouping", func(t *testing.T) {
		store := testStore(t)

		_, err := store.Connections(context.Background(), diagnostics.ConnectionsParams{GroupBy: "nonsense"})

		require.Error(t, err)
	})

	t.Run("connections lists sessions idle in a transaction", func(t *testing.T) {
		store := testStore(t)
		ctx := context.Background()
		conn := rawConn(t, ctx)

		pid := backendPID(t, ctx, conn)
		_, err := conn.ExecContext(ctx, "BEGIN")
		require.NoError(t, err)
		t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "ROLLBACK") })
		var one int
		require.NoError(t, conn.QueryRowContext(ctx, "SELECT 1").Scan(&one))

		var found *diagnostics.IdleInTx
		require.Eventually(t, func() bool {
			result, err := store.Connections(ctx, diagnostics.ConnectionsParams{GroupBy: "state", IdleInTxMinSec: 0})
			if err != nil {
				return false
			}
			for i := range result.IdleInTransaction {
				if result.IdleInTransaction[i].PID == pid {
					found = &result.IdleInTransaction[i]
					return true
				}
			}
			return false
		}, 5*time.Second, 100*time.Millisecond, "expected pid %d to be reported as idle in transaction", pid)

		assert.GreaterOrEqual(t, found.AgeSec, float64(0))
		assert.NotEmpty(t, found.User)
	})
}

func TestStoreReplication(t *testing.T) {
	t.Run("replication on a primary reports no standbys", func(t *testing.T) {
		store := testStore(t)

		result, err := store.Replication(context.Background())

		require.NoError(t, err)
		assert.True(t, result.IsPrimary)
		assert.NotNil(t, result.Standbys)
		assert.Empty(t, result.Standbys)
		assert.NotNil(t, result.Slots)
		assert.Empty(t, result.Slots)
		assert.GreaterOrEqual(t, result.WALRateBytesPerSec, float64(0))
	})
}

func TestStoreLockWaits(t *testing.T) {
	t.Run("lock waits reports a blocked session and its blocker", func(t *testing.T) {
		store := testStore(t)
		ctx := context.Background()
		seedLockFixture(t, ctx)

		blocker := rawConn(t, ctx)
		blockerPID := backendPID(t, ctx, blocker)
		_, err := blocker.ExecContext(ctx, "BEGIN")
		require.NoError(t, err)
		// Release the lock however this test ends, so that a failed
		// assertion cannot leave the blocked session hanging.
		defer func() { _, _ = blocker.ExecContext(context.Background(), "ROLLBACK") }()
		_, err = blocker.ExecContext(ctx, "LOCK TABLE pgmcp_test.lw_t IN ACCESS EXCLUSIVE MODE")
		require.NoError(t, err)

		blocked := rawConn(t, ctx)
		blockedPID := backendPID(t, ctx, blocked)
		blockedCtx, cancelBlocked := context.WithTimeout(context.Background(), 30*time.Second)
		done := make(chan error, 1)
		go func() {
			_, queryErr := blocked.ExecContext(blockedCtx, "SELECT * FROM pgmcp_test.lw_t")
			done <- queryErr
		}()
		drained := false
		// Cancel and reap the blocked query on every exit path, so a failed
		// assertion never leaves a query in flight on a connection the
		// cleanup is about to close.
		defer func() {
			cancelBlocked()
			if drained {
				return
			}
			select {
			case <-done:
			case <-time.After(10 * time.Second):
			}
		}()

		var edge *diagnostics.LockEdge
		require.Eventually(t, func() bool {
			result, err := store.LockWaits(ctx, diagnostics.LockWaitsParams{MinWaitMs: 0})
			if err != nil {
				return false
			}
			for i := range result.Edges {
				if result.Edges[i].BlockingPID == blockerPID && result.Edges[i].BlockedPID == blockedPID {
					edge = &result.Edges[i]
					return true
				}
			}
			return false
		}, 5*time.Second, 100*time.Millisecond, "expected pid %d to be reported as blocked by pid %d", blockedPID, blockerPID)

		assert.Equal(t, "relation", edge.LockType)
		assert.NotEmpty(t, edge.Mode)
		assert.Equal(t, "pgmcp_test.lw_t", edge.Relation)
		assert.Contains(t, edge.BlockedQuery, "lw_t")
		assert.NotEmpty(t, edge.BlockedUser)
		assert.NotEmpty(t, edge.BlockingUser)
		assert.GreaterOrEqual(t, edge.WaitMs, int64(0))

		_, err = blocker.ExecContext(ctx, "ROLLBACK")
		require.NoError(t, err)
		select {
		case err := <-done:
			drained = true
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("blocked session never completed after the blocker rolled back")
		}
	})

	t.Run("lock waits reports no edges and no cycles on an idle server", func(t *testing.T) {
		store := testStore(t)

		result, err := store.LockWaits(context.Background(), diagnostics.LockWaitsParams{MinWaitMs: 60_000})

		require.NoError(t, err)
		assert.NotNil(t, result.Edges)
		assert.Empty(t, result.Edges)
		assert.NotNil(t, result.Cycles)
		assert.Empty(t, result.Cycles)
	})
}

func TestStoreSettings(t *testing.T) {
	t.Run("settings returns work_mem with its unit and source", func(t *testing.T) {
		store := testStore(t)

		settings, err := store.Settings(context.Background())

		require.NoError(t, err)
		require.NotEmpty(t, settings)

		var workMem *diagnostics.Setting
		for i := range settings {
			if settings[i].Name == "work_mem" {
				workMem = &settings[i]
				break
			}
		}
		require.NotNil(t, workMem, "expected work_mem among %d settings", len(settings))
		assert.NotEmpty(t, workMem.Value)
		assert.Equal(t, "kB", workMem.Unit)
		assert.NotEmpty(t, workMem.Source)
		assert.NotEmpty(t, workMem.Category)
		assert.NotEmpty(t, workMem.ShortDesc)
	})
}

func TestStoreOverview(t *testing.T) {
	t.Run("overview reports database sizes and pg_stat_statements availability", func(t *testing.T) {
		store := testStore(t)

		overview, err := store.Overview(context.Background())

		require.NoError(t, err)
		assert.NotEmpty(t, overview.Server.Version)
		assert.Equal(t, store.database, overview.Server.Database)
		require.NotEmpty(t, overview.Databases)

		names := make([]string, 0, len(overview.Databases))
		for _, database := range overview.Databases {
			names = append(names, database.Name)
			assert.Greater(t, database.SizeBytes, int64(0))
		}
		assert.Contains(t, names, store.database)

		assert.Greater(t, overview.CacheHitRatio, float64(0))
		assert.LessOrEqual(t, overview.CacheHitRatio, float64(1))
		assert.Greater(t, overview.Connections, 0)
		assert.Greater(t, overview.MaxConnections, 0)
		assert.Equal(t, hasPgStatStatements(t, context.Background()), overview.PgStatStatements)
	})
}

// hasPgStatStatements reports whether the test database has the
// pg_stat_statements extension installed, read through a raw connection so
// the assertion does not depend on the code under test.
func hasPgStatStatements(t *testing.T, ctx context.Context) bool {
	t.Helper()

	var installed bool
	require.NoError(t, rawDB(t).QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM pg_extension WHERE extname='pg_stat_statements')").Scan(&installed))

	return installed
}
