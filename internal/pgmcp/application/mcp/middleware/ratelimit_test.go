package middleware

import (
	"context"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// principalRequest builds a tools/call request attributed to the given user id,
// or to an anonymous caller when userID is empty.
func principalRequest(userID string) *mcp.ServerRequest[*mcp.CallToolParamsRaw] {
	extra := &mcp.RequestExtra{}
	if userID != "" {
		extra.TokenInfo = &auth.TokenInfo{UserID: userID}
	}

	return &mcp.ServerRequest[*mcp.CallToolParamsRaw]{
		Params: &mcp.CallToolParamsRaw{Name: "pg_query"},
		Extra:  extra,
	}
}

// allowedHandler is a method handler that always succeeds.
func allowedHandler(calls *int) mcp.MethodHandler {
	return func(context.Context, string, mcp.Request) (mcp.Result, error) {
		*calls++

		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "fine"}}}, nil
	}
}

// isRateLimited reports whether the result is the over-limit tool result.
func isRateLimited(t *testing.T, result mcp.Result) bool {
	t.Helper()

	callResult, ok := result.(*mcp.CallToolResult)
	require.True(t, ok, "expected a *mcp.CallToolResult")

	if !callResult.IsError {
		return false
	}

	require.Len(t, callResult.Content, 1)
	text, ok := callResult.Content[0].(*mcp.TextContent)
	require.True(t, ok, "expected a *mcp.TextContent")
	assert.Equal(t, "rate limit exceeded, retry in 1s", text.Text)

	return true
}

func TestRateLimit(t *testing.T) {
	ctx := context.Background()

	t.Run("rate limit rejects the 61st call in a minute per principal and isolates principals", func(t *testing.T) {
		now := time.Now()
		limiter := newRateLimiter(60, 60, func() time.Time { return now })

		downstream := 0
		handler := limiter.middleware()(allowedHandler(&downstream))

		for i := range 60 {
			result, err := handler(ctx, methodCallTool, principalRequest("u1"))
			require.NoError(t, err)
			require.False(t, isRateLimited(t, result), "call %d should have been allowed", i+1)
		}

		result, err := handler(ctx, methodCallTool, principalRequest("u1"))
		require.NoError(t, err)
		assert.True(t, isRateLimited(t, result), "the 61st call should have been rejected")
		assert.Equal(t, 60, downstream, "a rejected call must not reach the handler")

		other, err := handler(ctx, methodCallTool, principalRequest("u2"))
		require.NoError(t, err)
		assert.False(t, isRateLimited(t, other), "a second principal has its own budget")
		assert.Equal(t, 61, downstream)
	})

	t.Run("rate limit buckets every unauthenticated caller as anonymous", func(t *testing.T) {
		now := time.Now()
		limiter := newRateLimiter(60, 1, func() time.Time { return now })

		downstream := 0
		handler := limiter.middleware()(allowedHandler(&downstream))

		first, err := handler(ctx, methodCallTool, principalRequest(""))
		require.NoError(t, err)
		require.False(t, isRateLimited(t, first))

		second, err := handler(ctx, methodCallTool, principalRequest(""))
		require.NoError(t, err)
		assert.True(t, isRateLimited(t, second))

		limiter.mu.Lock()
		defer limiter.mu.Unlock()
		assert.Contains(t, limiter.entries, anonymousPrincipal)
	})

	t.Run("rate limit refills the bucket as time passes", func(t *testing.T) {
		now := time.Now()
		limiter := newRateLimiter(60, 1, func() time.Time { return now })

		downstream := 0
		handler := limiter.middleware()(allowedHandler(&downstream))

		_, err := handler(ctx, methodCallTool, principalRequest("u1"))
		require.NoError(t, err)

		blocked, err := handler(ctx, methodCallTool, principalRequest("u1"))
		require.NoError(t, err)
		require.True(t, isRateLimited(t, blocked))

		now = now.Add(time.Second)

		refilled, err := handler(ctx, methodCallTool, principalRequest("u1"))
		require.NoError(t, err)
		assert.False(t, isRateLimited(t, refilled))
	})

	t.Run("rate limit evicts idle principals", func(t *testing.T) {
		now := time.Now()
		limiter := newRateLimiter(600000, evictEvery, func() time.Time { return now })

		downstream := 0
		handler := limiter.middleware()(allowedHandler(&downstream))

		_, err := handler(ctx, methodCallTool, principalRequest("idle"))
		require.NoError(t, err)

		now = now.Add(idleTTL + time.Minute)

		for range evictEvery - 1 {
			_, err = handler(ctx, methodCallTool, principalRequest("busy"))
			require.NoError(t, err)
		}

		limiter.mu.Lock()
		defer limiter.mu.Unlock()
		assert.NotContains(t, limiter.entries, "idle", "an idle principal should have been evicted")
		assert.Contains(t, limiter.entries, "busy")
		assert.Len(t, limiter.entries, 1)
	})

	t.Run("rate limit ignores methods other than tools/call", func(t *testing.T) {
		now := time.Now()
		limiter := newRateLimiter(60, 1, func() time.Time { return now })

		downstream := 0
		handler := limiter.middleware()(allowedHandler(&downstream))

		for range 5 {
			_, err := handler(ctx, methodReadResource, principalRequest("u1"))
			require.NoError(t, err)
		}

		assert.Equal(t, 5, downstream)

		limiter.mu.Lock()
		defer limiter.mu.Unlock()
		assert.Empty(t, limiter.entries)
	})

	t.Run("rate limit is a pass through when the limit is not positive", func(t *testing.T) {
		downstream := 0
		handler := RateLimit(0, 10)(allowedHandler(&downstream))

		for range 5 {
			result, err := handler(ctx, methodCallTool, principalRequest("u1"))
			require.NoError(t, err)
			require.False(t, isRateLimited(t, result))
		}

		assert.Equal(t, 5, downstream)
	})

	t.Run("rate limit exported constructor enforces the limit", func(t *testing.T) {
		downstream := 0
		handler := RateLimit(60, 1)(allowedHandler(&downstream))

		first, err := handler(ctx, methodCallTool, principalRequest("u1"))
		require.NoError(t, err)
		require.False(t, isRateLimited(t, first))

		second, err := handler(ctx, methodCallTool, principalRequest("u1"))
		require.NoError(t, err)
		assert.True(t, isRateLimited(t, second))
		assert.Equal(t, 1, downstream)
	})
}
