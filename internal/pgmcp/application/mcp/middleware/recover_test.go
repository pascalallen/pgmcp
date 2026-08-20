package middleware_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/application/mcp/middleware"
)

func TestRecover(t *testing.T) {
	t.Run("recover converts a panic into an error result for tools/call", func(t *testing.T) {
		buf := &bytes.Buffer{}
		log := slog.New(slog.NewJSONHandler(buf, nil))

		panicking := func(context.Context, string, mcp.Request) (mcp.Result, error) {
			panic("boom: SENTINEL_ARGUMENT_VALUE")
		}

		handler := middleware.Recover(log)(panicking)

		result, err := handler(context.Background(), methodCallTool, callToolRequest("query", "u1", nil))
		require.NoError(t, err)

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok, "expected a *mcp.CallToolResult")
		assert.True(t, callResult.IsError)
		require.Len(t, callResult.Content, 1)

		text, ok := callResult.Content[0].(*mcp.TextContent)
		require.True(t, ok, "expected a *mcp.TextContent")
		assert.Equal(t, "internal error", text.Text)
	})

	t.Run("recover logs the stack and panic type but never the panic value", func(t *testing.T) {
		buf := &bytes.Buffer{}
		log := slog.New(slog.NewJSONHandler(buf, nil))

		panicking := func(context.Context, string, mcp.Request) (mcp.Result, error) {
			panic("boom: SENTINEL_ARGUMENT_VALUE")
		}

		handler := middleware.Recover(log)(panicking)

		_, err := handler(context.Background(), methodCallTool, callToolRequest("query", "u1", nil))
		require.NoError(t, err)

		logged := buf.String()
		assert.Contains(t, logged, `"level":"ERROR"`)
		assert.Contains(t, logged, `"panic_type":"string"`)
		assert.Contains(t, logged, `"stack":`)
		assert.NotContains(t, logged, "SENTINEL_ARGUMENT_VALUE")
	})

	t.Run("recover turns a panic on other methods into a protocol error", func(t *testing.T) {
		panicking := func(context.Context, string, mcp.Request) (mcp.Result, error) {
			panic("boom")
		}

		handler := middleware.Recover(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))(panicking)

		result, err := handler(context.Background(), methodReadResource, callToolRequest("", "", nil))
		require.Error(t, err)
		assert.EqualError(t, err, "internal error")
		assert.Nil(t, result)
	})

	t.Run("recover passes a non-panicking handler through untouched", func(t *testing.T) {
		want := textResult("fine")

		handler := middleware.Recover(slog.New(slog.NewJSONHandler(&bytes.Buffer{}, nil)))(okHandler(want, nil))

		result, err := handler(context.Background(), methodCallTool, callToolRequest("query", "u1", nil))
		require.NoError(t, err)
		assert.Same(t, want, result)
	})

	t.Run("recover works without a logger", func(t *testing.T) {
		panicking := func(context.Context, string, mcp.Request) (mcp.Result, error) {
			panic("boom")
		}

		result, err := middleware.Recover(nil)(panicking)(context.Background(), methodCallTool, callToolRequest("query", "", nil))
		require.NoError(t, err)

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok, "expected a *mcp.CallToolResult")
		assert.True(t, callResult.IsError)
	})
}
