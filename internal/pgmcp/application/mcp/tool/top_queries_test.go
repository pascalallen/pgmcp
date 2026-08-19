package tool

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

func TestTopQueries(t *testing.T) {
	ctx := context.Background()

	t.Run("top queries defaults the ordering and the limit the port receives", func(t *testing.T) {
		var received diagnostics.TopQueriesParams

		_, handler := TopQueries(fakeDiag{topQueries: func(_ context.Context, p diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
			received = p

			return &diagnostics.TopQueriesResult{Available: true}, nil
		}})

		_, out, err := handler(ctx, nil, TopQueriesIn{})
		require.NoError(t, err)

		assert.Equal(t, diagnostics.OrderByTotalTime, received.OrderBy)
		assert.Equal(t, defaultTopQueriesLimit, received.Limit)
		assert.Equal(t, int64(0), received.MinCalls)
		assert.Empty(t, received.Database)
		assert.NotNil(t, out.Statements, "an empty statement list is never null")
	})

	t.Run("top queries clamps an oversized limit and a negative minimum call count", func(t *testing.T) {
		var received diagnostics.TopQueriesParams

		_, handler := TopQueries(fakeDiag{topQueries: func(_ context.Context, p diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
			received = p

			return &diagnostics.TopQueriesResult{}, nil
		}})

		_, _, err := handler(ctx, nil, TopQueriesIn{OrderBy: "mean_time", Limit: 5000, MinCalls: -10, Database: "pgmcp"})
		require.NoError(t, err)

		assert.Equal(t, diagnostics.OrderByMeanTime, received.OrderBy)
		assert.Equal(t, maxTopQueriesLimit, received.Limit)
		assert.Equal(t, int64(0), received.MinCalls)
		assert.Equal(t, "pgmcp", received.Database)
	})

	t.Run("top queries passes a limit inside the bounds through untouched", func(t *testing.T) {
		var received diagnostics.TopQueriesParams

		_, handler := TopQueries(fakeDiag{topQueries: func(_ context.Context, p diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
			received = p

			return &diagnostics.TopQueriesResult{}, nil
		}})

		_, _, err := handler(ctx, nil, TopQueriesIn{Limit: 7, MinCalls: 3})
		require.NoError(t, err)

		assert.Equal(t, 7, received.Limit)
		assert.Equal(t, int64(3), received.MinCalls)
	})

	t.Run("top queries rejects an unknown ordering with a tool error before calling the port", func(t *testing.T) {
		called := false

		_, handler := TopQueries(fakeDiag{topQueries: func(context.Context, diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
			called = true

			return &diagnostics.TopQueriesResult{}, nil
		}})

		_, _, err := handler(ctx, nil, TopQueriesIn{OrderBy: "wall_clock"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), `unsupported order_by "wall_clock"`)
		assert.Contains(t, err.Error(), "shared_blks_read")
		assert.False(t, called, "the port must not be reached for an invalid ordering")
	})

	t.Run("top queries returns the structured statistics with the meta block", func(t *testing.T) {
		statsSince := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)

		_, handler := TopQueries(fakeDiag{topQueries: func(context.Context, diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
			return &diagnostics.TopQueriesResult{
				Available:  true,
				StatsSince: &statsSince,
				Statements: []diagnostics.StatementStat{{QueryID: 42, Query: "SELECT $1", Calls: 9, TotalMs: 120}},
			}, nil
		}})

		_, out, err := handler(ctx, nil, TopQueriesIn{})
		require.NoError(t, err)

		assert.True(t, out.Available)
		assert.Equal(t, &statsSince, out.StatsSince)
		require.Len(t, out.Statements, 1)
		assert.Equal(t, int64(42), out.Statements[0].QueryID)
		assert.Equal(t, "16.4", out.ServerVersion)
		assert.Equal(t, "pgmcp", out.Database)
		assert.False(t, out.GeneratedAt.IsZero())
	})

	t.Run("top queries reports a missing extension as unavailable with its hint", func(t *testing.T) {
		_, handler := TopQueries(fakeDiag{topQueries: func(context.Context, diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
			return &diagnostics.TopQueriesResult{Hint: "Run `CREATE EXTENSION pg_stat_statements;`"}, nil
		}})

		_, out, err := handler(ctx, nil, TopQueriesIn{})
		require.NoError(t, err)

		assert.False(t, out.Available)
		assert.Contains(t, out.Hint, "CREATE EXTENSION")
		assert.Empty(t, out.Statements)
		assert.NotNil(t, out.Statements)
	})

	t.Run("top queries still answers when the server version probe fails", func(t *testing.T) {
		_, handler := TopQueries(fakeDiag{
			serverInfo: func(context.Context) (*diagnostics.ServerInfo, error) {
				return nil, errors.New("probe unavailable")
			},
			topQueries: func(context.Context, diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
				return &diagnostics.TopQueriesResult{Available: true}, nil
			},
		})

		_, out, err := handler(ctx, nil, TopQueriesIn{})
		require.NoError(t, err)

		assert.True(t, out.Available)
		assert.Empty(t, out.ServerVersion)
		assert.Empty(t, out.Database)
		assert.False(t, out.GeneratedAt.IsZero())
	})

	t.Run("top queries treats a nil result as an empty listing", func(t *testing.T) {
		_, handler := TopQueries(fakeDiag{topQueries: func(context.Context, diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
			return nil, nil
		}})

		_, out, err := handler(ctx, nil, TopQueriesIn{})
		require.NoError(t, err)
		assert.NotNil(t, out.Statements)
		assert.Empty(t, out.Statements)
	})

	t.Run("top queries propagates a port failure", func(t *testing.T) {
		failure := errors.New("pg_stat_statements probe failed")

		_, handler := TopQueries(fakeDiag{topQueries: func(context.Context, diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
			return nil, failure
		}})

		_, _, err := handler(ctx, nil, TopQueriesIn{})
		require.ErrorIs(t, err, failure)
	})

	t.Run("top queries answers over an mcp session and lists itself as read only", func(t *testing.T) {
		definition, handler := TopQueries(fakeDiag{topQueries: func(context.Context, diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
			return &diagnostics.TopQueriesResult{
				Available:  true,
				Statements: []diagnostics.StatementStat{{QueryID: 7, Query: "SELECT $1", Calls: 3, MeanMs: 2.5, HitRatio: 0.98}},
			}, nil
		}})

		session := serveTool(t, definition, handler)

		out := callStructured[TopQueriesOut](t, session, "top_queries", TopQueriesIn{OrderBy: "calls", Limit: 5})
		assert.True(t, out.Available)
		require.Len(t, out.Statements, 1)
		assert.Equal(t, int64(7), out.Statements[0].QueryID)
		assert.Equal(t, 0.98, out.Statements[0].HitRatio)
		assert.Equal(t, "16.4", out.ServerVersion)

		requireReadOnlyListing(t, session, "top_queries", "Top queries")
	})

	t.Run("top queries reports an unknown ordering as a tool error over an mcp session", func(t *testing.T) {
		definition, handler := TopQueries(fakeDiag{})

		session := serveTool(t, definition, handler)

		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "top_queries", Arguments: TopQueriesIn{OrderBy: "nope"}})
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, contentText(result), "unsupported order_by")
	})
}
