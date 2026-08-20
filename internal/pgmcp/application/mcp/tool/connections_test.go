package tool

import (
	"context"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

func TestConnections(t *testing.T) {
	ctx := context.Background()

	t.Run("connections defaults the grouping and the idle in transaction threshold", func(t *testing.T) {
		var received diagnostics.ConnectionsParams

		_, handler := Connections(fakeDiag{connections: func(_ context.Context, p diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error) {
			received = p

			return &diagnostics.ConnectionsResult{}, nil
		}})

		_, _, err := handler(ctx, nil, ConnectionsIn{})
		require.NoError(t, err)

		assert.Equal(t, defaultConnectionsGroupBy, received.GroupBy)
		assert.Equal(t, defaultConnectionsIdleInTxMinSec, received.IdleInTxMinSec)
	})

	t.Run("connections passes an explicit grouping and threshold through", func(t *testing.T) {
		var received diagnostics.ConnectionsParams

		_, handler := Connections(fakeDiag{connections: func(_ context.Context, p diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error) {
			received = p

			return &diagnostics.ConnectionsResult{}, nil
		}})

		_, _, err := handler(ctx, nil, ConnectionsIn{GroupBy: "wait_event", IdleInTxMinSec: 5})
		require.NoError(t, err)

		assert.Equal(t, diagnostics.GroupBy("wait_event"), received.GroupBy)
		assert.Equal(t, 5, received.IdleInTxMinSec)
	})

	t.Run("connections replaces a negative threshold with the default", func(t *testing.T) {
		var received diagnostics.ConnectionsParams

		_, handler := Connections(fakeDiag{connections: func(_ context.Context, p diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error) {
			received = p

			return &diagnostics.ConnectionsResult{}, nil
		}})

		_, _, err := handler(ctx, nil, ConnectionsIn{IdleInTxMinSec: -30})
		require.NoError(t, err)
		assert.Equal(t, defaultConnectionsIdleInTxMinSec, received.IdleInTxMinSec)
	})

	t.Run("connections rejects an unknown grouping with a tool error before calling the port", func(t *testing.T) {
		called := false

		_, handler := Connections(fakeDiag{connections: func(context.Context, diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error) {
			called = true

			return &diagnostics.ConnectionsResult{}, nil
		}})

		_, _, err := handler(ctx, nil, ConnectionsIn{GroupBy: "hostname"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unsupported group_by "hostname"`)
		assert.Contains(t, err.Error(), "wait_event")
		assert.False(t, called, "the port must not be reached for an invalid grouping")
	})

	t.Run("connections returns the grouped counts and idle sessions with the meta block", func(t *testing.T) {
		_, handler := Connections(fakeDiag{connections: func(context.Context, diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error) {
			return &diagnostics.ConnectionsResult{
				Groups:            []diagnostics.ConnGroup{{Key: "active", Count: 12}, {Key: "idle", Count: 30}},
				Total:             42,
				MaxConnections:    100,
				UsedPct:           42,
				IdleInTransaction: []diagnostics.IdleInTx{{PID: 77, AgeSec: 610, User: "app", Application: "worker", Query: "SELECT 1"}},
			}, nil
		}})

		_, out, err := handler(ctx, nil, ConnectionsIn{})
		require.NoError(t, err)

		require.Len(t, out.Groups, 2)
		assert.Equal(t, "active", out.Groups[0].Key)
		assert.Equal(t, 42, out.Total)
		assert.Equal(t, 100, out.MaxConnections)
		assert.Equal(t, 42.0, out.UsedPct)
		require.Len(t, out.IdleInTransaction, 1)
		assert.Equal(t, 77, out.IdleInTransaction[0].PID)
		assert.Equal(t, "16.4", out.ServerVersion)
		assert.False(t, out.GeneratedAt.IsZero())
	})

	t.Run("connections reports empty lists rather than null", func(t *testing.T) {
		_, handler := Connections(fakeDiag{connections: func(context.Context, diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error) {
			return &diagnostics.ConnectionsResult{Total: 1, MaxConnections: 100}, nil
		}})

		_, out, err := handler(ctx, nil, ConnectionsIn{})
		require.NoError(t, err)

		assert.NotNil(t, out.Groups)
		assert.NotNil(t, out.IdleInTransaction)
		assert.Empty(t, out.Groups)
		assert.Empty(t, out.IdleInTransaction)
	})

	t.Run("connections treats a nil result as an empty summary", func(t *testing.T) {
		_, handler := Connections(fakeDiag{connections: func(context.Context, diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error) {
			return nil, nil
		}})

		_, out, err := handler(ctx, nil, ConnectionsIn{})
		require.NoError(t, err)
		assert.NotNil(t, out.Groups)
		assert.NotNil(t, out.IdleInTransaction)
	})

	t.Run("connections propagates a port failure", func(t *testing.T) {
		failure := errors.New("pg_stat_activity unavailable")

		_, handler := Connections(fakeDiag{connections: func(context.Context, diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error) {
			return nil, failure
		}})

		_, _, err := handler(ctx, nil, ConnectionsIn{})
		require.ErrorIs(t, err, failure)
	})

	t.Run("connections answers over an mcp session and lists itself as read only", func(t *testing.T) {
		definition, handler := Connections(fakeDiag{connections: func(context.Context, diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error) {
			return &diagnostics.ConnectionsResult{
				Groups:            []diagnostics.ConnGroup{{Key: "idle in transaction", Count: 3}},
				Total:             3,
				MaxConnections:    100,
				UsedPct:           3,
				IdleInTransaction: []diagnostics.IdleInTx{{PID: 9, AgeSec: 120, User: "app"}},
			}, nil
		}})

		session := serveTool(t, definition, handler)

		out := callStructured[ConnectionsOut](t, session, "connections", ConnectionsIn{GroupBy: "state", IdleInTxMinSec: 30})
		require.Len(t, out.Groups, 1)
		assert.Equal(t, 3, out.Groups[0].Count)
		require.Len(t, out.IdleInTransaction, 1)
		assert.Equal(t, 9, out.IdleInTransaction[0].PID)
		assert.Equal(t, "pgmcp", out.Database)

		requireReadOnlyListing(t, session, "connections", "Connections")
	})

	t.Run("connections reports an unknown grouping as a tool error over an mcp session", func(t *testing.T) {
		definition, handler := Connections(fakeDiag{})

		session := serveTool(t, definition, handler)

		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "connections", Arguments: ConnectionsIn{GroupBy: "hostname"}})
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, contentText(result), "unsupported group_by")
	})
}
