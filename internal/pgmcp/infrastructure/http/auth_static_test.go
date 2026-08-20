package httpserver

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticVerifier(t *testing.T) {
	req := httptest.NewRequest("POST", "/mcp", nil)

	t.Run("accepts a configured key and identifies it by a hash prefix", func(t *testing.T) {
		verify := StaticVerifier([]string{"first-key", "second-key"})

		info, err := verify(context.Background(), "second-key", req)

		require.NoError(t, err)
		require.NotNil(t, info)
		assert.True(t, strings.HasPrefix(info.UserID, "key:"), "user id should be a key fingerprint, got %q", info.UserID)
		assert.Len(t, info.UserID, len("key:")+8)
		assert.NotContains(t, info.UserID, "second-key", "the key itself must never appear in the principal id")
	})

	t.Run("gives every accepted key an expiration the bearer middleware can enforce", func(t *testing.T) {
		verify := StaticVerifier([]string{"a-key"})

		info, err := verify(context.Background(), "a-key", req)

		require.NoError(t, err)
		assert.False(t, info.Expiration.IsZero(), "a zero expiration is rejected by RequireBearerToken")
		assert.WithinDuration(t, time.Now().Add(24*time.Hour), info.Expiration, time.Minute)
	})

	t.Run("gives two different keys two different principal ids", func(t *testing.T) {
		verify := StaticVerifier([]string{"first-key", "second-key"})

		first, err := verify(context.Background(), "first-key", req)
		require.NoError(t, err)
		second, err := verify(context.Background(), "second-key", req)
		require.NoError(t, err)

		assert.NotEqual(t, first.UserID, second.UserID)
	})

	t.Run("rejects an unknown key with an error the bearer middleware understands", func(t *testing.T) {
		verify := StaticVerifier([]string{"first-key"})

		info, err := verify(context.Background(), "not-the-key", req)

		require.Error(t, err)
		assert.Nil(t, info)
		assert.True(t, errors.Is(err, auth.ErrInvalidToken), "error must unwrap to auth.ErrInvalidToken, got %v", err)
		assert.NotContains(t, err.Error(), "not-the-key", "the presented token must never appear in the error")
	})

	t.Run("rejects a key that is a prefix of a configured key", func(t *testing.T) {
		verify := StaticVerifier([]string{"first-key"})

		_, err := verify(context.Background(), "first", req)

		require.Error(t, err)
		assert.True(t, errors.Is(err, auth.ErrInvalidToken))
	})

	t.Run("rejects every token when no keys are configured", func(t *testing.T) {
		verify := StaticVerifier(nil)

		_, err := verify(context.Background(), "anything", req)

		require.Error(t, err)
		assert.True(t, errors.Is(err, auth.ErrInvalidToken))
	})

	t.Run("rejects the empty token even when an empty key was configured", func(t *testing.T) {
		verify := StaticVerifier([]string{"", "real-key"})

		_, err := verify(context.Background(), "", req)

		require.Error(t, err)
		assert.True(t, errors.Is(err, auth.ErrInvalidToken))
	})
}
