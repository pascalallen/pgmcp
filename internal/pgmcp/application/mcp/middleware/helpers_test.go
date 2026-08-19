package middleware_test

import (
	"context"
	"encoding/json"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	methodCallTool     = "tools/call"
	methodReadResource = "resources/read"
)

// callToolRequest builds a tools/call request for the named tool, carrying the
// given raw arguments and an optional authenticated user id.
func callToolRequest(tool, userID string, arguments any) *mcp.ServerRequest[*mcp.CallToolParamsRaw] {
	params := &mcp.CallToolParamsRaw{Name: tool}

	if arguments != nil {
		raw, err := json.Marshal(arguments)
		if err != nil {
			panic(err)
		}

		params.Arguments = raw
	}

	extra := &mcp.RequestExtra{}
	if userID != "" {
		extra.TokenInfo = &auth.TokenInfo{UserID: userID}
	}

	return &mcp.ServerRequest[*mcp.CallToolParamsRaw]{Params: params, Extra: extra}
}

// okHandler returns a method handler that reports the given result and error.
func okHandler(result mcp.Result, err error) mcp.MethodHandler {
	return func(context.Context, string, mcp.Request) (mcp.Result, error) {
		return result, err
	}
}

// textResult builds a successful tools/call result carrying one text block.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: text}}}
}
