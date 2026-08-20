package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/MicahParks/keyfunc/v3"
	"github.com/golang-jwt/jwt/v5"
	"github.com/modelcontextprotocol/go-sdk/auth"
)

// jwtAlgorithms are the signing algorithms a token may use. The list is
// asymmetric only: an HMAC token would be verifiable with the public key the
// JWKS publishes, which is exactly the alg-confusion attack. "none" is absent
// for the same reason.
var jwtAlgorithms = []string{
	"RS256", "RS384", "RS512",
	"ES256", "ES384", "ES512",
	"PS256", "PS384", "PS512",
}

// scopeClaim is the space-separated OAuth 2.0 scope claim (RFC 8693 §4.2).
const scopeClaim = "scope"

// NewJWTVerifier builds a token verifier that validates RS/ES/PS-signed JWTs
// against the JWK set published at jwksURL, requiring the given issuer,
// audience, and a present, unexpired exp claim.
//
// The key set is fetched here and refreshed thereafter by a background
// goroutine. That goroutine lives until the returned shutdown function is
// called or ctx is cancelled, whichever comes first, so callers should pass
// the application context and defer the shutdown function.
//
// An unreachable or malformed JWKS is deliberately not a startup error: the
// refresher retries on its own, and refusing to boot would turn a momentary
// identity-provider blip into an outage that outlives it. Until a key set
// arrives the verifier holds no keys, so it rejects every token — the server
// starts closed, not open.
//
// Every rejection wraps auth.ErrInvalidToken, which is what the bearer
// middleware matches on to answer 401 rather than 500. The presented token is
// never logged or placed in an error; the jwt library's own error text names
// only the claim or signature that failed.
func NewJWTVerifier(ctx context.Context, jwksURL, issuer, audience string) (auth.TokenVerifier, func(), error) {
	if jwksURL == "" || issuer == "" || audience == "" {
		return nil, nil, errors.New("httpserver: jwt auth needs a jwks url, an issuer and an audience")
	}

	ctx, cancel := context.WithCancel(ctx)

	keys, err := keyfunc.NewDefaultCtx(ctx, []string{jwksURL})
	if err != nil {
		cancel()

		return nil, nil, fmt.Errorf("httpserver: load jwks: %w", err)
	}

	verify := func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		parsed, err := jwt.Parse(token, keys.Keyfunc,
			jwt.WithIssuer(issuer),
			jwt.WithAudience(audience),
			jwt.WithExpirationRequired(),
			jwt.WithValidMethods(jwtAlgorithms),
		)
		if err != nil {
			return nil, fmt.Errorf("%w: %s", auth.ErrInvalidToken, err)
		}

		subject, err := parsed.Claims.GetSubject()
		if err != nil {
			return nil, fmt.Errorf("%w: %s", auth.ErrInvalidToken, err)
		}

		expiry, err := parsed.Claims.GetExpirationTime()
		if err != nil || expiry == nil {
			return nil, fmt.Errorf("%w: missing expiration", auth.ErrInvalidToken)
		}

		return &auth.TokenInfo{
			UserID:     subject,
			Scopes:     tokenScopes(parsed.Claims),
			Expiration: expiry.Time,
		}, nil
	}

	return verify, cancel, nil
}

// tokenScopes reads the space-separated scope claim, yielding an empty (never
// nil) slice when the token carries no scopes.
func tokenScopes(claims jwt.Claims) []string {
	mapped, ok := claims.(jwt.MapClaims)
	if !ok {
		return []string{}
	}

	raw, ok := mapped[scopeClaim].(string)
	if !ok {
		return []string{}
	}

	return strings.Fields(raw)
}
