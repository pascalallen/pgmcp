// Package httpserver exposes the pgmcp MCP server over the Streamable HTTP
// transport, behind bearer authentication (a static API key list or JWTs
// validated against a JWK set), alongside the RFC 9728 protected resource
// metadata document clients use to discover the authorization server, and an
// unauthenticated health probe.
//
// Nothing in this package logs, echoes, or embeds a bearer token, and no
// response body carries the text of an internal error.
package httpserver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/modelcontextprotocol/go-sdk/oauthex"

	"github.com/pascalallen/pgmcp/internal/pgmcp/infrastructure/config"
)

const (
	// mcpPath is where the Streamable HTTP transport is mounted.
	mcpPath = "/mcp"
	// prmPath is the RFC 9728 well-known location of the protected resource
	// metadata document.
	prmPath = "/.well-known/oauth-protected-resource"
	// healthPath is the unauthenticated liveness/readiness probe.
	healthPath = "GET /healthz"
	// resourceName is the human-readable name in the metadata document.
	resourceName = "pgmcp"

	// maxRequestBodyBytes caps an incoming JSON-RPC request. A diagnostic
	// call carries a SQL statement at most; a megabyte is generous, and the
	// cap is enforced during the read, so a chunked or HTTP/2 body cannot
	// slip past it.
	maxRequestBodyBytes = 1 << 20
	// bearerClockSkew is the tolerance applied to a token's expiry, covering
	// a few seconds of drift between this server's clock and the issuer's.
	bearerClockSkew = 30 * time.Second

	// readHeaderTimeout bounds how long a client may take to send its
	// request line and headers.
	readHeaderTimeout = 10 * time.Second
	// readTimeout bounds how long a client may take to send a whole request.
	readTimeout = 30 * time.Second
	// idleTimeout bounds how long a keep-alive connection may sit unused.
	idleTimeout = 120 * time.Second
)

// Options configure the HTTP surface: where it will listen, how callers
// authenticate, what the OAuth metadata advertises, and how health is
// determined.
type Options struct {
	Listen      string          // listen address, used only for the fail-secure loopback check
	ResourceURL string          // public URL this server is reachable at
	AuthMode    config.AuthMode // "none", "static", or "jwt"
	APIKeys     []string        // accepted keys when AuthMode is static
	JWKSURL     string          // JWK set location when AuthMode is jwt
	JWTIssuer   string          // required iss claim when AuthMode is jwt
	JWTAudience string          // required aud claim when AuthMode is jwt

	AuthServers    []string                    // OAuth authorization servers to advertise
	InsecureNoAuth bool                        // operator's explicit opt-in to unauthenticated non-loopback serving
	Log            *slog.Logger                // structured logger, may be nil
	Health         func(context.Context) error // backing-store probe for /healthz, may be nil
}

// NewHandler builds the HTTP handler serving the MCP endpoint, the protected
// resource metadata document, and the health probe.
//
// This is the HTTP transport's entry point and nothing else: a server running
// over stdio never calls NewHandler, so none of the authentication modes below
// apply to it. Its single caller is the parent process that launched it, and
// the operating system, not a bearer token, decides who that is.
//
// The returned shutdown function is always non-nil and safe to call more than
// once; it stops the background JWKS refresh when AuthMode is jwt and does
// nothing otherwise, so callers can defer it unconditionally. ctx bounds that
// refresh goroutine and should be the application context.
//
// Construction fails secure: an unrecognized auth mode is refused, static
// auth with no keys is refused, and auth mode none is refused on a
// non-loopback listen address unless the operator explicitly opted in. The
// configuration loader refuses the same combination; this is the second lock
// on the same door, because a handler built by any other caller must be no
// easier to leave open.
func NewHandler(ctx context.Context, server *mcp.Server, o Options) (http.Handler, func(), error) {
	streamable := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{
		Stateless:                    true,
		Logger:                       o.Log,
		MaxRequestBodyBytes:          maxRequestBodyBytes,
		PropagateRequestCancellation: true,
	})

	shutdown := func() {}
	var handler http.Handler = streamable

	switch o.AuthMode {
	case config.AuthModeStatic:
		if len(o.APIKeys) == 0 {
			return nil, nil, errors.New("httpserver: auth mode static needs at least one api key")
		}
		handler = auth.RequireBearerToken(StaticVerifier(o.APIKeys), o.bearerOptions())(handler)

	case config.AuthModeJWT:
		verify, stop, err := NewJWTVerifier(ctx, o.JWKSURL, o.JWTIssuer, o.JWTAudience)
		if err != nil {
			return nil, nil, err
		}
		shutdown = stop
		handler = auth.RequireBearerToken(verify, o.bearerOptions())(handler)

	case config.AuthModeNone:
		if !o.InsecureNoAuth && !isLoopback(o.Listen) {
			return nil, nil, fmt.Errorf("httpserver: refusing to serve unauthenticated on non-loopback address %q; set an auth mode or opt in explicitly with insecure-no-auth", o.Listen)
		}
		if o.Log != nil {
			o.Log.Warn("serving mcp without authentication", "listen", o.Listen)
		}

	default:
		return nil, nil, fmt.Errorf("httpserver: unknown auth mode %q", o.AuthMode)
	}

	mux := http.NewServeMux()
	mux.Handle(mcpPath, handler)
	mux.Handle(healthPath, healthHandler(o.Health, o.Log))

	if len(o.AuthServers) > 0 {
		mux.Handle(prmPath, auth.ProtectedResourceMetadataHandler(&oauthex.ProtectedResourceMetadata{
			Resource:               o.ResourceURL,
			AuthorizationServers:   o.AuthServers,
			BearerMethodsSupported: []string{"header"},
			ResourceName:           resourceName,
		}))
	}

	return mux, shutdown, nil
}

// bearerOptions builds the options for the bearer middleware. The metadata URL
// is advertised in the 401 challenge only when there is a metadata document to
// point at — pointing a client at a 404 is worse than saying nothing.
func (o Options) bearerOptions() *auth.RequireBearerTokenOptions {
	opts := &auth.RequireBearerTokenOptions{ClockSkew: bearerClockSkew}
	if len(o.AuthServers) > 0 && o.ResourceURL != "" {
		opts.ResourceMetadataURL = strings.TrimSuffix(o.ResourceURL, "/") + prmPath
	}

	return opts
}

// NewServer wraps h in an http.Server with the timeouts a public listener
// needs. WriteTimeout is deliberately zero: a streamable response is an
// open-ended SSE stream, and a write deadline would cut it off mid-session.
// ReadHeaderTimeout, ReadTimeout, and IdleTimeout still bound every phase a
// slow or idle client can hold a connection through.
func NewServer(listen string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              listen,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      0,
		IdleTimeout:       idleTimeout,
	}
}

// isLoopback reports whether hostport's host is "localhost" or a loopback IP
// address. A hostport without a port (or an unparseable one) is treated as a
// bare host, and anything that is not demonstrably loopback — including the
// wildcard ":8080" — is treated as public.
func isLoopback(hostport string) bool {
	host, _, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}
