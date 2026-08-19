package middleware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Recover converts a panic in a downstream handler into a well-formed
// response: a tools/call panic becomes an error tool result the model can see,
// and any other method becomes a generic protocol error. The panic value is
// never surfaced to the caller and never logged — it may embed tool arguments
// or SQL text — so only its type and the goroutine stack are recorded.
func Recover(log *slog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (result mcp.Result, err error) {
			defer func() {
				recovered := recover()
				if recovered == nil {
					return
				}

				if log != nil {
					attrs := []any{
						slog.String("method", method),
						slog.String("panic_type", fmt.Sprintf("%T", recovered)),
						slog.String("stack", string(debug.Stack())),
					}
					if tool := toolName(method, req); tool != "" {
						attrs = append(attrs, slog.String("tool", tool))
					}

					log.ErrorContext(ctx, "panic recovered in mcp handler", attrs...)
				}

				if method == methodCallTool {
					result, err = toolError("internal error"), nil

					return
				}

				result, err = nil, errors.New("internal error")
			}()

			return next(ctx, method, req)
		}
	}
}
