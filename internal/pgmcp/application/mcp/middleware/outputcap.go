package middleware

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// OutputCap bounds the structured content of a tools/call result. A payload
// larger than maxBytes is replaced wholesale with a marker telling the model
// to narrow its request; the call is still a success, so the model can retry
// with a tighter filter instead of treating the cap as a failure. A
// non-positive maxBytes disables the cap.
func OutputCap(maxBytes int) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		if maxBytes <= 0 {
			return next
		}

		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil || method != methodCallTool {
				return result, err
			}

			callResult, ok := result.(*mcp.CallToolResult)
			if !ok || callResult == nil || callResult.StructuredContent == nil {
				return result, err
			}

			encoded, marshalErr := json.Marshal(callResult.StructuredContent)
			if marshalErr != nil || len(encoded) <= maxBytes {
				return result, err
			}

			replacement := map[string]any{
				"truncated": true,
				"reason":    fmt.Sprintf("output exceeded %d bytes; narrow the request", maxBytes),
			}

			marker, marshalErr := json.Marshal(replacement)
			if marshalErr != nil {
				return result, err
			}

			callResult.StructuredContent = json.RawMessage(marker)
			callResult.Content = []mcp.Content{&mcp.TextContent{Text: string(marker)}}
			callResult.IsError = false

			return callResult, nil
		}
	}
}
