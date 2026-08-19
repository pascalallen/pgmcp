package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

func TestLockWaits(t *testing.T) {
	ctx := context.Background()

	t.Run("lock waits passes the minimum wait through and clamps a negative one", func(t *testing.T) {
		var received diagnostics.LockWaitsParams

		_, handler := LockWaits(fakeDiag{lockWaits: func(_ context.Context, p diagnostics.LockWaitsParams) (*diagnostics.LockWaitsResult, error) {
			received = p

			return &diagnostics.LockWaitsResult{}, nil
		}})

		_, _, err := handler(ctx, nil, LockWaitsIn{MinWaitMs: 250})
		require.NoError(t, err)
		assert.Equal(t, int64(250), received.MinWaitMs)

		_, _, err = handler(ctx, nil, LockWaitsIn{MinWaitMs: -5})
		require.NoError(t, err)
		assert.Equal(t, int64(0), received.MinWaitMs)
	})

	t.Run("lock waits returns the wait graph with the meta block", func(t *testing.T) {
		_, handler := LockWaits(fakeDiag{lockWaits: func(context.Context, diagnostics.LockWaitsParams) (*diagnostics.LockWaitsResult, error) {
			return &diagnostics.LockWaitsResult{
				Edges: []diagnostics.LockEdge{{
					BlockedPID: 101, BlockedQuery: "UPDATE orders", BlockedUser: "app",
					BlockingPID: 202, BlockingQuery: "SELECT 1", BlockingUser: "admin",
					LockType: "relation", Mode: "RowExclusiveLock", Relation: "orders", WaitMs: 4200,
				}},
				Cycles: [][]int{{101, 202}},
			}, nil
		}})

		_, out, err := handler(ctx, nil, LockWaitsIn{})
		require.NoError(t, err)

		require.Len(t, out.Edges, 1)
		assert.Equal(t, 101, out.Edges[0].BlockedPID)
		assert.Equal(t, 202, out.Edges[0].BlockingPID)
		require.Len(t, out.Cycles, 1)
		assert.Equal(t, []int{101, 202}, out.Cycles[0])
		assert.Equal(t, "16.4", out.ServerVersion)
		assert.False(t, out.GeneratedAt.IsZero())
	})

	t.Run("lock waits reports an idle server as empty lists rather than null", func(t *testing.T) {
		_, handler := LockWaits(fakeDiag{lockWaits: func(context.Context, diagnostics.LockWaitsParams) (*diagnostics.LockWaitsResult, error) {
			return &diagnostics.LockWaitsResult{}, nil
		}})

		_, out, err := handler(ctx, nil, LockWaitsIn{})
		require.NoError(t, err)

		assert.NotNil(t, out.Edges)
		assert.NotNil(t, out.Cycles)
		assert.Empty(t, out.Edges)
		assert.Empty(t, out.Cycles)
	})

	t.Run("lock waits treats a nil result as an idle server", func(t *testing.T) {
		_, handler := LockWaits(fakeDiag{lockWaits: func(context.Context, diagnostics.LockWaitsParams) (*diagnostics.LockWaitsResult, error) {
			return nil, nil
		}})

		_, out, err := handler(ctx, nil, LockWaitsIn{})
		require.NoError(t, err)
		assert.NotNil(t, out.Edges)
		assert.NotNil(t, out.Cycles)
	})

	t.Run("lock waits propagates a port failure", func(t *testing.T) {
		failure := errors.New("pg_locks unavailable")

		_, handler := LockWaits(fakeDiag{lockWaits: func(context.Context, diagnostics.LockWaitsParams) (*diagnostics.LockWaitsResult, error) {
			return nil, failure
		}})

		_, _, err := handler(ctx, nil, LockWaitsIn{})
		require.ErrorIs(t, err, failure)
	})

	t.Run("lock waits answers over an mcp session and lists itself as read only", func(t *testing.T) {
		definition, handler := LockWaits(fakeDiag{lockWaits: func(context.Context, diagnostics.LockWaitsParams) (*diagnostics.LockWaitsResult, error) {
			return &diagnostics.LockWaitsResult{
				Edges:  []diagnostics.LockEdge{{BlockedPID: 11, BlockingPID: 22, LockType: "transactionid", Mode: "ShareLock", WaitMs: 900}},
				Cycles: [][]int{},
			}, nil
		}})

		session := serveTool(t, definition, handler)

		out := callStructured[LockWaitsOut](t, session, "lock_waits", LockWaitsIn{MinWaitMs: 500})
		require.Len(t, out.Edges, 1)
		assert.Equal(t, 22, out.Edges[0].BlockingPID)
		assert.Empty(t, out.Cycles)
		assert.Equal(t, "pgmcp", out.Database)

		requireReadOnlyListing(t, session, "lock_waits", "Lock waits")
	})
}
