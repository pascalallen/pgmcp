package httpserver

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testKeyID    = "test-key-1"
	testIssuer   = "https://issuer.test/"
	testAudience = "pgmcp"
)

// jwksFixture is an in-test signing key together with the JWKS endpoint that
// publishes its public half.
type jwksFixture struct {
	key *rsa.PrivateKey
	url string
}

// newJWKSFixture generates a throwaway RSA key and serves its public half as a
// JWKS document, so the verifier fetches real keys over real HTTP.
func newJWKSFixture(t *testing.T) *jwksFixture {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	n := base64.RawURLEncoding.EncodeToString(key.PublicKey.N.Bytes())
	e := base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes())
	document := fmt.Sprintf(`{"keys":[{"kty":"RSA","use":"sig","alg":"RS256","kid":%q,"n":%q,"e":%q}]}`, testKeyID, n, e)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(document))
	}))
	t.Cleanup(srv.Close)

	return &jwksFixture{key: key, url: srv.URL}
}

// sign issues an RS256 token carrying claims, signed by the fixture's key.
func (f *jwksFixture) sign(t *testing.T, claims jwt.MapClaims) string {
	t.Helper()

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	token.Header["kid"] = testKeyID
	signed, err := token.SignedString(f.key)
	require.NoError(t, err)

	return signed
}

// validClaims are the claims of a token the verifier should accept.
func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"iss":   testIssuer,
		"aud":   testAudience,
		"sub":   "user-123",
		"scope": "pgmcp:read pgmcp:diagnose",
		"exp":   time.Now().Add(time.Hour).Unix(),
		"iat":   time.Now().Add(-time.Minute).Unix(),
	}
}

// newTestVerifier builds a verifier against the fixture and shuts its refresh
// goroutine down when the test ends.
func newTestVerifier(t *testing.T, f *jwksFixture) auth.TokenVerifier {
	t.Helper()

	verify, shutdown, err := NewJWTVerifier(context.Background(), f.url, testIssuer, testAudience)
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	t.Cleanup(shutdown)

	return verify
}

func TestNewJWTVerifier(t *testing.T) {
	req := httptest.NewRequest("POST", "/mcp", nil)

	t.Run("requires a jwks url, an issuer and an audience", func(t *testing.T) {
		for name, args := range map[string][3]string{
			"no jwks url": {"", testIssuer, testAudience},
			"no issuer":   {"https://jwks.test/keys", "", testAudience},
			"no audience": {"https://jwks.test/keys", testIssuer, ""},
		} {
			t.Run(name, func(t *testing.T) {
				verify, shutdown, err := NewJWTVerifier(context.Background(), args[0], args[1], args[2])

				require.Error(t, err)
				assert.Nil(t, verify)
				assert.Nil(t, shutdown)
			})
		}
	})

	t.Run("accepts a valid token and reports its subject, scopes and expiry", func(t *testing.T) {
		f := newJWKSFixture(t)
		verify := newTestVerifier(t, f)
		claims := validClaims()

		info, err := verify(context.Background(), f.sign(t, claims), req)

		require.NoError(t, err)
		require.NotNil(t, info)
		assert.Equal(t, "user-123", info.UserID)
		assert.Equal(t, []string{"pgmcp:read", "pgmcp:diagnose"}, info.Scopes)
		assert.WithinDuration(t, time.Unix(claims["exp"].(int64), 0), info.Expiration, time.Second)
	})

	t.Run("accepts a valid token whose audience claim is a list containing ours", func(t *testing.T) {
		f := newJWKSFixture(t)
		verify := newTestVerifier(t, f)
		claims := validClaims()
		claims["aud"] = []string{"someone-else", testAudience}

		info, err := verify(context.Background(), f.sign(t, claims), req)

		require.NoError(t, err)
		assert.Equal(t, "user-123", info.UserID)
	})

	t.Run("returns an empty scope list when the token carries no scope claim", func(t *testing.T) {
		f := newJWKSFixture(t)
		verify := newTestVerifier(t, f)
		claims := validClaims()
		delete(claims, "scope")

		info, err := verify(context.Background(), f.sign(t, claims), req)

		require.NoError(t, err)
		assert.Empty(t, info.Scopes)
	})

	t.Run("rejects a token issued for another audience", func(t *testing.T) {
		f := newJWKSFixture(t)
		verify := newTestVerifier(t, f)
		claims := validClaims()
		claims["aud"] = "some-other-service"

		info, err := verify(context.Background(), f.sign(t, claims), req)

		requireInvalidToken(t, info, err)
	})

	t.Run("rejects a token from another issuer", func(t *testing.T) {
		f := newJWKSFixture(t)
		verify := newTestVerifier(t, f)
		claims := validClaims()
		claims["iss"] = "https://evil.test/"

		info, err := verify(context.Background(), f.sign(t, claims), req)

		requireInvalidToken(t, info, err)
	})

	t.Run("rejects an expired token", func(t *testing.T) {
		f := newJWKSFixture(t)
		verify := newTestVerifier(t, f)
		claims := validClaims()
		claims["exp"] = time.Now().Add(-time.Hour).Unix()

		info, err := verify(context.Background(), f.sign(t, claims), req)

		requireInvalidToken(t, info, err)
	})

	t.Run("rejects a token with no expiry at all", func(t *testing.T) {
		f := newJWKSFixture(t)
		verify := newTestVerifier(t, f)
		claims := validClaims()
		delete(claims, "exp")

		info, err := verify(context.Background(), f.sign(t, claims), req)

		requireInvalidToken(t, info, err)
	})

	t.Run("rejects an unsigned alg=none token", func(t *testing.T) {
		f := newJWKSFixture(t)
		verify := newTestVerifier(t, f)
		token := jwt.NewWithClaims(jwt.SigningMethodNone, validClaims())
		token.Header["kid"] = testKeyID
		unsigned, err := token.SignedString(jwt.UnsafeAllowNoneSignatureType)
		require.NoError(t, err)

		info, err := verify(context.Background(), unsigned, req)

		requireInvalidToken(t, info, err)
	})

	t.Run("rejects a token signed by a key the jwks does not publish", func(t *testing.T) {
		f := newJWKSFixture(t)
		verify := newTestVerifier(t, f)
		other := newJWKSFixture(t)

		info, err := verify(context.Background(), other.sign(t, validClaims()), req)

		requireInvalidToken(t, info, err)
	})

	t.Run("rejects a token that is not a jwt at all", func(t *testing.T) {
		f := newJWKSFixture(t)
		verify := newTestVerifier(t, f)

		info, err := verify(context.Background(), "not-a-token", req)

		requireInvalidToken(t, info, err)
	})
}

// requireInvalidToken asserts a rejection the bearer middleware understands,
// carrying no token information.
func requireInvalidToken(t *testing.T, info *auth.TokenInfo, err error) {
	t.Helper()

	require.Error(t, err)
	assert.Nil(t, info)
	assert.True(t, errors.Is(err, auth.ErrInvalidToken), "error must unwrap to auth.ErrInvalidToken, got %v", err)
}
