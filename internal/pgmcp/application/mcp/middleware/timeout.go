package middleware

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Timeout bounds the context of the two methods that can reach the database —
// tools/call and resources/read — so a wedged query cannot pin a connection
// forever. Every other method, and a non-positive duration, passes straight
// through.
func Timeout(d time.Duration) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			if d <= 0 || (method != methodCallTool && method != methodReadResource) {
				return next(ctx, method, req)
			}

			ctx, cancel := context.WithTimeout(ctx, d)
			defer cancel()

			return next(ctx, method, req)
		}
	}
}
