package middleware_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/application/mcp/middleware"
)

// bigIn is the (empty) input of the oversized test tool.
type bigIn struct{}

// bigOut is the structured output of the oversized test tool.
type bigOut struct {
	Rows []string `json:"rows"`
}

// structuredResult builds a tools/call result whose structured content is v,
// mirrored into a single JSON text block the way the SDK does.
func structuredResult(t *testing.T, v any) *mcp.CallToolResult {
	t.Helper()

	raw, err := json.Marshal(v)
	require.NoError(t, err)

	return &mcp.CallToolResult{
		Content:           []mcp.Content{&mcp.TextContent{Text: string(raw)}},
		StructuredContent: json.RawMessage(raw),
	}
}

func TestOutputCap(t *testing.T) {
	ctx := context.Background()

	t.Run("output cap replaces oversized structured content", func(t *testing.T) {
		oversized := structuredResult(t, map[string]any{"rows": strings.Repeat("SENTINEL_ROW_VALUE", 100)})

		handler := middleware.OutputCap(64)(okHandler(oversized, nil))

		result, err := handler(ctx, methodCallTool, callToolRequest("pg_query", "u1", nil))
		require.NoError(t, err)

		callResult, ok := result.(*mcp.CallToolResult)
		require.True(t, ok, "expected a *mcp.CallToolResult")
		assert.False(t, callResult.IsError, "a truncated result is not an error")

		raw, err := json.Marshal(callResult.StructuredContent)
		require.NoError(t, err)

		replacement := map[string]any{}
		require.NoError(t, json.Unmarshal(raw, &replacement))
		assert.Equal(t, true, replacement["truncated"])
		assert.Equal(t, "output exceeded 64 bytes; narrow the request", replacement["reason"])

		require.Len(t, callResult.Content, 1)
		text, ok := callResult.Content[0].(*mcp.TextContent)
		require.True(t, ok, "expected a *mcp.TextContent")
		assert.JSONEq(t, string(raw), text.Text)
		assert.NotContains(t, text.Text, "SENTINEL_ROW_VALUE")
	})

	t.Run("output cap leaves results within the cap untouched", func(t *testing.T) {
		small := structuredResult(t, map[string]any{"rows": []string{"a"}})

		handler := middleware.OutputCap(1024)(okHandler(small, nil))

		result, err := handler(ctx, methodCallTool, callToolRequest("pg_query", "u1", nil))
		require.NoError(t, err)
		assert.Same(t, small, result)
		assert.JSONEq(t, `{"rows":["a"]}`, string(small.StructuredContent.(json.RawMessage)))
	})

	t.Run("output cap is a pass through for other methods and non positive caps", func(t *testing.T) {
		oversized := structuredResult(t, map[string]any{"rows": strings.Repeat("x", 100)})

		result, err := middleware.OutputCap(8)(okHandler(oversized, nil))(ctx, methodReadResource, callToolRequest("", "", nil))
		require.NoError(t, err)
		assert.Same(t, oversized, result)

		result, err = middleware.OutputCap(0)(okHandler(oversized, nil))(ctx, methodCallTool, callToolRequest("pg_query", "", nil))
		require.NoError(t, err)
		assert.Same(t, oversized, result)
	})

	t.Run("output cap leaves results without structured content alone", func(t *testing.T) {
		plain := textResult(strings.Repeat("x", 500))

		result, err := middleware.OutputCap(8)(okHandler(plain, nil))(ctx, methodCallTool, callToolRequest("pg_query", "", nil))
		require.NoError(t, err)
		assert.Same(t, plain, result)
	})

	t.Run("output cap caps a typed tool result over a live server session", func(t *testing.T) {
		rows := make([]string, 200)
		for i := range rows {
			rows[i] = "SENTINEL_ROW_VALUE"
		}

		server := mcp.NewServer(&mcp.Implementation{Name: "pgmcp-test", Version: "v0.0.1"}, nil)
		mcp.AddTool(server, &mcp.Tool{Name: "big", Description: "returns a lot of rows"},
			func(context.Context, *mcp.CallToolRequest, bigIn) (*mcp.CallToolResult, bigOut, error) {
				return nil, bigOut{Rows: rows}, nil
			})
		server.AddReceivingMiddleware(middleware.OutputCap(256))

		clientTransport, serverTransport := mcp.NewInMemoryTransports()

		serverSession, err := server.Connect(ctx, serverTransport, nil)
		require.NoError(t, err)
		defer func() { _ = serverSession.Wait() }()

		client := mcp.NewClient(&mcp.Implementation{Name: "pgmcp-test-client", Version: "v0.0.1"}, nil)

		clientSession, err := client.Connect(ctx, clientTransport, nil)
		require.NoError(t, err)
		defer func() { _ = clientSession.Close() }()

		result, err := clientSession.CallTool(ctx, &mcp.CallToolParams{Name: "big"})
		require.NoError(t, err)
		assert.False(t, result.IsError)

		raw, err := json.Marshal(result.StructuredContent)
		require.NoError(t, err)
		assert.NotContains(t, string(raw), "SENTINEL_ROW_VALUE")

		replacement := map[string]any{}
		require.NoError(t, json.Unmarshal(raw, &replacement))
		assert.Equal(t, true, replacement["truncated"])
		assert.Equal(t, "output exceeded 256 bytes; narrow the request", replacement["reason"])

		require.NotEmpty(t, result.Content)
		text, ok := result.Content[0].(*mcp.TextContent)
		require.True(t, ok, "expected a *mcp.TextContent")
		assert.NotContains(t, text.Text, "SENTINEL_ROW_VALUE")
	})

	t.Run("output cap leaves a result it cannot marshal alone", func(t *testing.T) {
		unmarshalable := &mcp.CallToolResult{StructuredContent: make(chan int)}

		result, err := middleware.OutputCap(8)(okHandler(unmarshalable, nil))(ctx, methodCallTool, callToolRequest("pg_query", "", nil))
		require.NoError(t, err)
		assert.Same(t, unmarshalable, result)
	})

	t.Run("output cap leaves a failed call alone", func(t *testing.T) {
		oversized := structuredResult(t, map[string]any{"rows": strings.Repeat("x", 100)})
		failure := errors.New("boom")

		result, err := middleware.OutputCap(8)(okHandler(oversized, failure))(ctx, methodCallTool, callToolRequest("pg_query", "", nil))
		require.ErrorIs(t, err, failure)
		assert.Same(t, oversized, result)
	})
}
