package middleware

import (
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
)

func TestToolName(t *testing.T) {
	t.Run("tool name is empty for a nil request or a non tools/call request", func(t *testing.T) {
		assert.Empty(t, toolName(methodCallTool, nil))
		assert.Empty(t, toolName(methodReadResource, principalRequest("u1")))
	})

	t.Run("tool name is empty when the params are not tool call params", func(t *testing.T) {
		req := &mcp.ServerRequest[*mcp.ReadResourceParams]{Params: &mcp.ReadResourceParams{URI: "pgmcp://schema"}}

		assert.Empty(t, toolName(methodCallTool, req))
	})

	t.Run("tool name reads the invoked tool", func(t *testing.T) {
		assert.Equal(t, "pg_query", toolName(methodCallTool, principalRequest("u1")))
	})
}

func TestPrincipalID(t *testing.T) {
	t.Run("principal id is empty without a request, extra, or token", func(t *testing.T) {
		assert.Empty(t, principalID(nil))
		assert.Empty(t, principalID(&mcp.ClientRequest[*mcp.CallToolParamsRaw]{}))
		assert.Empty(t, principalID(principalRequest("")))
	})

	t.Run("principal id reads the authenticated user", func(t *testing.T) {
		assert.Equal(t, "u1", principalID(principalRequest("u1")))
	})
}

func TestRetrySeconds(t *testing.T) {
	t.Run("retry seconds rounds up and never advises less than a second", func(t *testing.T) {
		assert.Equal(t, 1, retrySeconds(0))
		assert.Equal(t, 1, retrySeconds(-time.Second))
		assert.Equal(t, 1, retrySeconds(10*time.Millisecond))
		assert.Equal(t, 2, retrySeconds(1500*time.Millisecond))
	})
}
