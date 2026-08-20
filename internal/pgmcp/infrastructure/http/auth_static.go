package httpserver

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
)

const (
	// staticTokenTTL is the lifetime stamped on an accepted static key. A
	// static key has no expiry of its own, but RequireBearerToken rejects a
	// TokenInfo with a zero Expiration, so one has to be supplied. It is a
	// formality, not a revocation mechanism: revoking a static key means
	// removing it from the configuration and restarting.
	staticTokenTTL = 24 * time.Hour
	// principalIDPrefix marks a principal as a static API key rather than a
	// user from an identity provider.
	principalIDPrefix = "key:"
	// principalIDHexLen is how much of a key's hash goes into its principal
	// id — enough to tell configured keys apart in logs and rate-limit
	// buckets, far too little to attack the key itself.
	principalIDHexLen = 8
)

// StaticVerifier builds a token verifier over a fixed set of API keys.
//
// The keys are hashed once at construction and never held in memory in the
// clear beyond that; verification hashes the presented token and compares it
// against every stored hash with a constant-time compare, without an early
// exit, so neither the time to answer nor the number of comparisons reveals
// which key was closest. The token itself is never logged, echoed, or placed
// in an error.
func StaticVerifier(keys []string) auth.TokenVerifier {
	hashes := make([][sha256.Size]byte, 0, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		hashes = append(hashes, sha256.Sum256([]byte(key)))
	}

	return func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		presented := sha256.Sum256([]byte(token))

		matched := 0
		for i := range hashes {
			matched |= subtle.ConstantTimeCompare(hashes[i][:], presented[:])
		}

		if matched == 0 {
			return nil, fmt.Errorf("%w: unknown api key", auth.ErrInvalidToken)
		}

		return &auth.TokenInfo{
			UserID:     principalIDPrefix + hex.EncodeToString(presented[:])[:principalIDHexLen],
			Expiration: time.Now().Add(staticTokenTTL),
		}, nil
	}
}
