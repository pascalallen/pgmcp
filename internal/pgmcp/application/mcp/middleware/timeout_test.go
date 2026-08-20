package middleware_test

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/application/mcp/middleware"
)

func TestTimeout(t *testing.T) {
	t.Run("timeout gives tools/call a bounded context", func(t *testing.T) {
		var deadline time.Time
		var hasDeadline bool

		capture := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			deadline, hasDeadline = ctx.Deadline()

			return textResult("fine"), nil
		}

		handler := middleware.Timeout(50 * time.Millisecond)(capture)

		_, err := handler(context.Background(), methodCallTool, callToolRequest("pg_query", "u1", nil))
		require.NoError(t, err)
		require.True(t, hasDeadline, "expected the handler context to carry a deadline")
		assert.LessOrEqual(t, time.Until(deadline), 50*time.Millisecond)
	})

	t.Run("timeout gives resources/read a bounded context", func(t *testing.T) {
		var hasDeadline bool

		capture := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			_, hasDeadline = ctx.Deadline()

			return textResult("fine"), nil
		}

		handler := middleware.Timeout(50 * time.Millisecond)(capture)

		_, err := handler(context.Background(), methodReadResource, callToolRequest("", "", nil))
		require.NoError(t, err)
		assert.True(t, hasDeadline)
	})

	t.Run("timeout cancels the context once the deadline passes", func(t *testing.T) {
		slow := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			<-ctx.Done()

			return nil, ctx.Err()
		}

		handler := middleware.Timeout(10 * time.Millisecond)(slow)

		_, err := handler(context.Background(), methodCallTool, callToolRequest("pg_query", "u1", nil))
		require.ErrorIs(t, err, context.DeadlineExceeded)
	})

	t.Run("timeout cancels the context when the handler returns", func(t *testing.T) {
		var inner context.Context

		capture := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			inner = ctx

			return textResult("fine"), nil
		}

		handler := middleware.Timeout(time.Minute)(capture)

		_, err := handler(context.Background(), methodCallTool, callToolRequest("pg_query", "u1", nil))
		require.NoError(t, err)
		require.NotNil(t, inner)
		assert.ErrorIs(t, inner.Err(), context.Canceled)
	})

	t.Run("timeout leaves other methods and non positive durations alone", func(t *testing.T) {
		var hasDeadline bool

		capture := func(ctx context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
			_, hasDeadline = ctx.Deadline()

			return textResult("fine"), nil
		}

		_, err := middleware.Timeout(time.Minute)(capture)(context.Background(), "tools/list", callToolRequest("", "", nil))
		require.NoError(t, err)
		assert.False(t, hasDeadline)

		_, err = middleware.Timeout(0)(capture)(context.Background(), methodCallTool, callToolRequest("pg_query", "", nil))
		require.NoError(t, err)
		assert.False(t, hasDeadline)
	})
}
