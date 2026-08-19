// Package middleware provides the receiving middleware pgmcp installs on its
// MCP server: panic recovery, structured call logging, per-call timeouts,
// per-principal rate limiting, and a cap on structured output size.
//
// Nothing in this package ever logs tool arguments, SQL text, result rows, or
// error text — a call record carries only {method, tool, duration_ms, ok,
// user_id}. The package depends on the standard library and the MCP SDK only.
package middleware

import (
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	// methodCallTool is the JSON-RPC method of an MCP tool invocation.
	methodCallTool = "tools/call"
	// methodReadResource is the JSON-RPC method of an MCP resource read.
	methodReadResource = "resources/read"
)

// toolName returns the name of the tool being invoked, or an empty string when
// the request is not a tools/call request.
func toolName(method string, req mcp.Request) string {
	if method != methodCallTool || req == nil {
		return ""
	}

	params, ok := req.GetParams().(*mcp.CallToolParamsRaw)
	if !ok || params == nil {
		return ""
	}

	return params.Name
}

// principalID returns the authenticated user id carried by the request, or an
// empty string when the caller is unauthenticated.
func principalID(req mcp.Request) string {
	if req == nil {
		return ""
	}

	extra := req.GetExtra()
	if extra == nil || extra.TokenInfo == nil {
		return ""
	}

	return extra.TokenInfo.UserID
}

// toolError builds a tools/call result reporting text as a tool-level error,
// which the model can see and correct, rather than a protocol error.
func toolError(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}
