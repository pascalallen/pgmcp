package middleware

import (
	"context"
	"log/slog"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Logging records one Info record per handled call. It deliberately logs no
// request parameters, no result content, and no error text: any of those can
// echo tool arguments or SQL. A failed call is reported as ok=false and
// nothing more.
//
// The record is emitted from a defer, so a call that panics past this
// middleware — Recover sits outside it — is still accounted for, as ok=false
// with the duration it burned.
func Logging(log *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		if log == nil {
			return next
		}

		return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
			started := time.Now()
			returned := false

			defer func() {
				// A call that never returned panicked past this middleware; its
				// named results are still zero, so completion is tracked
				// explicitly rather than inferred from them.
				attrs := []any{
					slog.String("method", method),
					slog.Float64("duration_ms", float64(time.Since(started).Microseconds())/1000),
					slog.Bool("ok", returned && err == nil && !isErrorResult(result)),
				}
				if tool := toolName(method, req); tool != "" {
					attrs = append(attrs, slog.String("tool", tool))
				}
				if user := principalID(req); user != "" {
					attrs = append(attrs, slog.String("user_id", user))
				}

				log.InfoContext(ctx, "mcp call", attrs...)
			}()

			result, err = next(ctx, method, req)
			returned = true

			return result, err
		}
	}
}

// isErrorResult reports whether the result is a tools/call result flagged as a
// tool-level error.
func isErrorResult(result mcp.Result) bool {
	callResult, ok := result.(*mcp.CallToolResult)

	return ok && callResult != nil && callResult.IsError
}
