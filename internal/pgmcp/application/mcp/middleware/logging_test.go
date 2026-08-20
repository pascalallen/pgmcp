package middleware_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/application/mcp/middleware"
)

// decodeRecord parses the single JSON log record written to buf.
func decodeRecord(t *testing.T, buf *bytes.Buffer) map[string]any {
	t.Helper()

	record := map[string]any{}
	require.NoError(t, json.Unmarshal(buf.Bytes(), &record))

	return record
}

func TestLogging(t *testing.T) {
	t.Run("logging records tool name duration and user id but not arguments", func(t *testing.T) {
		buf := &bytes.Buffer{}
		log := slog.New(slog.NewJSONHandler(buf, nil))

		req := callToolRequest("pg_query", "u1", map[string]string{"sql": "SENTINEL_ARGUMENT_VALUE"})

		handler := middleware.Logging(log)(okHandler(textResult("SENTINEL_ROW_VALUE"), nil))

		_, err := handler(context.Background(), methodCallTool, req)
		require.NoError(t, err)

		record := decodeRecord(t, buf)
		assert.Equal(t, "mcp call", record["msg"])
		assert.Equal(t, methodCallTool, record["method"])
		assert.Equal(t, "pg_query", record["tool"])
		assert.Equal(t, "u1", record["user_id"])
		assert.Equal(t, true, record["ok"])
		assert.Contains(t, record, "duration_ms")

		logged := buf.String()
		assert.NotContains(t, logged, "SENTINEL_ARGUMENT_VALUE")
		assert.NotContains(t, logged, "SENTINEL_ROW_VALUE")
	})

	t.Run("logging reports ok false without echoing the error text", func(t *testing.T) {
		buf := &bytes.Buffer{}
		log := slog.New(slog.NewJSONHandler(buf, nil))

		handler := middleware.Logging(log)(okHandler(nil, errors.New("failed on SENTINEL_ERROR_TEXT")))

		_, err := handler(context.Background(), methodCallTool, callToolRequest("pg_query", "u1", nil))
		require.Error(t, err)

		record := decodeRecord(t, buf)
		assert.Equal(t, false, record["ok"])
		assert.NotContains(t, buf.String(), "SENTINEL_ERROR_TEXT")
	})

	t.Run("logging reports ok false when the tool result is an error result", func(t *testing.T) {
		buf := &bytes.Buffer{}
		log := slog.New(slog.NewJSONHandler(buf, nil))

		failed := &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "SENTINEL_ERROR_TEXT"}}}

		handler := middleware.Logging(log)(okHandler(failed, nil))

		_, err := handler(context.Background(), methodCallTool, callToolRequest("pg_query", "u1", nil))
		require.NoError(t, err)

		record := decodeRecord(t, buf)
		assert.Equal(t, false, record["ok"])
		assert.NotContains(t, buf.String(), "SENTINEL_ERROR_TEXT")
	})

	t.Run("logging omits tool and user id for anonymous non tool calls", func(t *testing.T) {
		buf := &bytes.Buffer{}
		log := slog.New(slog.NewJSONHandler(buf, nil))

		handler := middleware.Logging(log)(okHandler(textResult("fine"), nil))

		_, err := handler(context.Background(), methodReadResource, callToolRequest("pg_query", "", nil))
		require.NoError(t, err)

		record := decodeRecord(t, buf)
		assert.Equal(t, methodReadResource, record["method"])
		assert.NotContains(t, record, "tool")
		assert.NotContains(t, record, "user_id")
	})

	t.Run("logging is a pass through without a logger", func(t *testing.T) {
		want := textResult("fine")

		result, err := middleware.Logging(nil)(okHandler(want, nil))(context.Background(), methodCallTool, callToolRequest("pg_query", "u1", nil))
		require.NoError(t, err)
		assert.Same(t, want, result)
	})

	t.Run("logging measures the real duration of the call", func(t *testing.T) {
		buf := &bytes.Buffer{}
		log := slog.New(slog.NewJSONHandler(buf, nil))

		slow := func(context.Context, string, mcp.Request) (mcp.Result, error) {
			time.Sleep(5 * time.Millisecond)

			return textResult("fine"), nil
		}

		handler := middleware.Logging(log)(slow)

		_, err := handler(context.Background(), methodCallTool, callToolRequest("pg_query", "u1", nil))
		require.NoError(t, err)

		record := decodeRecord(t, buf)
		duration, ok := record["duration_ms"].(float64)
		require.True(t, ok, "duration_ms should be a number")
		assert.Greater(t, duration, 4.0, "a 5ms handler must not be logged as instantaneous")
	})

	t.Run("logging still records a call that panics past it", func(t *testing.T) {
		buf := &bytes.Buffer{}
		log := slog.New(slog.NewJSONHandler(buf, nil))

		panicking := func(context.Context, string, mcp.Request) (mcp.Result, error) {
			panic("boom: SENTINEL_ERROR_TEXT")
		}

		// Recover sits outside Logging in the installed order.
		handler := middleware.Recover(nil)(middleware.Logging(log)(panicking))

		_, err := handler(context.Background(), methodCallTool, callToolRequest("pg_query", "u1", nil))
		require.NoError(t, err)

		record := decodeRecord(t, buf)
		assert.Equal(t, "mcp call", record["msg"])
		assert.Equal(t, "pg_query", record["tool"])
		assert.Equal(t, "u1", record["user_id"])
		assert.Equal(t, false, record["ok"])
		assert.Contains(t, record, "duration_ms")
		assert.NotContains(t, buf.String(), "SENTINEL_ERROR_TEXT")
	})
}
