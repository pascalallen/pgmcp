package tool

import "github.com/modelcontextprotocol/go-sdk/mcp"

// readOnly builds the annotation block every tool in this package carries:
// the server only ever reads, so repeating a call changes nothing and the
// tool's world is closed to the one database it is connected to.
func readOnly(title string) *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{
		Title:           title,
		ReadOnlyHint:    true,
		DestructiveHint: ptr(false),
		IdempotentHint:  true,
		OpenWorldHint:   ptr(false),
	}
}

// ptr returns a pointer to v, for the annotation fields the MCP spec models
// as optional booleans.
func ptr[T any](v T) *T { return &v }
