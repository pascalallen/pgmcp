package httpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/application/mcp/tool"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/sqlguard"
	mcpserver "github.com/pascalallen/pgmcp/internal/pgmcp/infrastructure/mcp"
)

// errStub is what the stub port answers with; no test drives a tool handler,
// so no test ever sees it.
var errStub = errors.New("stub diagnostics")

// stubDiagnostics satisfies the read-only diagnostics port without a database.
// The transport tests only list the tool surface; they never call a handler.
type stubDiagnostics struct{}

func (stubDiagnostics) ServerInfo(context.Context) (*diagnostics.ServerInfo, error) {
	return nil, errStub
}
func (stubDiagnostics) Overview(context.Context) (*diagnostics.Overview, error) { return nil, errStub }
func (stubDiagnostics) Settings(context.Context) ([]diagnostics.Setting, error) { return nil, errStub }
func (stubDiagnostics) TopQueries(context.Context, diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
	return nil, errStub
}
func (stubDiagnostics) Explain(context.Context, diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
	return nil, errStub
}
func (stubDiagnostics) LockWaits(context.Context, diagnostics.LockWaitsParams) (*diagnostics.LockWaitsResult, error) {
	return nil, errStub
}
func (stubDiagnostics) IndexHealth(context.Context, diagnostics.IndexHealthParams) (*diagnostics.IndexHealthResult, error) {
	return nil, errStub
}
func (stubDiagnostics) TableHealth(context.Context, diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
	return nil, errStub
}
func (stubDiagnostics) Connections(context.Context, diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error) {
	return nil, errStub
}
func (stubDiagnostics) Replication(context.Context) (*diagnostics.ReplicationResult, error) {
	return nil, errStub
}
func (stubDiagnostics) Query(context.Context, diagnostics.QueryParams) (*diagnostics.QueryResult, error) {
	return nil, errStub
}

// stubParser satisfies the SQL guard's parser port without libpg_query.
type stubParser struct{}

func (stubParser) Parse(string) (*sqlguard.Statement, error) { return nil, errStub }

// discardLog is a logger whose output goes nowhere, so a test's failure output
// stays readable.
func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testMCPServer builds a fully wired pgmcp server over the stub port.
func testMCPServer(t *testing.T) *mcp.Server {
	t.Helper()

	return mcpserver.New(mcpserver.Params{
		Version:         "test",
		Diag:            stubDiagnostics{},
		Parser:          stubParser{},
		Log:             discardLog(),
		CallTimeout:     5 * time.Second,
		RateLimitPerMin: 60,
		MaxOutputBytes:  1 << 20,
		Tools:           tool.Options{},
		HTTP:            true,
	})
}

// serve builds the handler under o and puts it behind a live HTTP server.
func serve(t *testing.T, o Options) *httptest.Server {
	t.Helper()

	handler, shutdown, err := NewHandler(context.Background(), testMCPServer(t), o)
	require.NoError(t, err)
	require.NotNil(t, shutdown)
	t.Cleanup(shutdown)

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv
}

// bearerTransport injects a bearer token into every outgoing request.
type bearerTransport struct {
	token string
	base  http.RoundTripper
}

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)

	return b.base.RoundTrip(clone)
}

// listTools runs a real MCP session over the streamable transport and returns
// the names the server advertises.
func listTools(t *testing.T, endpoint, token string) []string {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "pgmcp-test-client", Version: "test"}, nil)
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             endpoint,
		HTTPClient:           &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}},
		DisableStandaloneSSE: true,
	}, nil)
	require.NoError(t, err)
	defer func() { _ = session.Close() }()

	result, err := session.ListTools(ctx, nil)
	require.NoError(t, err)

	names := make([]string, 0, len(result.Tools))
	for _, tl := range result.Tools {
		names = append(names, tl.Name)
	}

	return names
}

// staticOptions are the options of a server behind a single static API key,
// advertising an authorization server so RFC 9728 discovery is in play.
func staticOptions() Options {
	return Options{
		Listen:      "127.0.0.1:0",
		ResourceURL: "https://pgmcp.test",
		AuthMode:    "static",
		APIKeys:     []string{"correct-horse-battery-staple"},
		AuthServers: []string{"https://issuer.test/"},
		Log:         discardLog(),
		Health:      func(context.Context) error { return nil },
	}
}

func TestNewHandler(t *testing.T) {
	t.Run("refuses to build for a non-loopback listen with auth mode none", func(t *testing.T) {
		handler, shutdown, err := NewHandler(context.Background(), testMCPServer(t), Options{
			Listen:   "0.0.0.0:8080",
			AuthMode: "none",
			Log:      discardLog(),
		})

		require.Error(t, err)
		assert.Nil(t, handler)
		assert.Nil(t, shutdown)
		assert.Contains(t, err.Error(), "loopback")
	})

	t.Run("builds for a non-loopback listen with auth mode none once the operator opts in", func(t *testing.T) {
		handler, shutdown, err := NewHandler(context.Background(), testMCPServer(t), Options{
			Listen:         "0.0.0.0:8080",
			AuthMode:       "none",
			InsecureNoAuth: true,
			Log:            discardLog(),
		})

		require.NoError(t, err)
		require.NotNil(t, handler)
		require.NotNil(t, shutdown)
		shutdown()
	})

	t.Run("builds for a loopback listen with auth mode none", func(t *testing.T) {
		handler, shutdown, err := NewHandler(context.Background(), testMCPServer(t), Options{
			Listen:   "127.0.0.1:8080",
			AuthMode: "none",
			Log:      discardLog(),
		})

		require.NoError(t, err)
		require.NotNil(t, handler)
		shutdown()
	})

	t.Run("refuses to build for auth mode static with no keys", func(t *testing.T) {
		handler, shutdown, err := NewHandler(context.Background(), testMCPServer(t), Options{
			Listen:   "127.0.0.1:8080",
			AuthMode: "static",
			Log:      discardLog(),
		})

		require.Error(t, err)
		assert.Nil(t, handler)
		assert.Nil(t, shutdown)
	})

	t.Run("refuses to build for an unrecognized auth mode", func(t *testing.T) {
		handler, shutdown, err := NewHandler(context.Background(), testMCPServer(t), Options{
			Listen:   "127.0.0.1:8080",
			AuthMode: "",
			Log:      discardLog(),
		})

		require.Error(t, err)
		assert.Nil(t, handler)
		assert.Nil(t, shutdown)
	})

	t.Run("refuses to build for auth mode jwt with no issuer or audience", func(t *testing.T) {
		handler, shutdown, err := NewHandler(context.Background(), testMCPServer(t), Options{
			Listen:   "127.0.0.1:8080",
			AuthMode: "jwt",
			JWKSURL:  "https://jwks.test/keys",
			Log:      discardLog(),
		})

		require.Error(t, err)
		assert.Nil(t, handler)
		assert.Nil(t, shutdown)
	})

	t.Run("still builds when the jwks is unreachable, and rejects every token until it arrives", func(t *testing.T) {
		f := newJWKSFixture(t)
		token := f.sign(t, validClaims())

		handler, shutdown, err := NewHandler(context.Background(), testMCPServer(t), Options{
			Listen:      "127.0.0.1:0",
			AuthMode:    "jwt",
			JWKSURL:     "http://127.0.0.1:1/keys",
			JWTIssuer:   testIssuer,
			JWTAudience: testAudience,
			Log:         discardLog(),
		})
		require.NoError(t, err, "a momentary identity-provider outage must not stop the server booting")
		require.NotNil(t, handler)
		t.Cleanup(shutdown)

		srv := httptest.NewServer(handler)
		t.Cleanup(srv.Close)

		req, err := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(`{}`))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode, "with no keys the server must start closed, not open")
	})
}

func TestHandlerAuth(t *testing.T) {
	t.Run("rejects a POST to /mcp with no bearer token and points at the resource metadata", func(t *testing.T) {
		srv := serve(t, staticOptions())

		resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		challenge := resp.Header.Get("WWW-Authenticate")
		assert.Contains(t, challenge, "Bearer")
		assert.Contains(t, challenge, "resource_metadata")
		assert.Contains(t, challenge, "https://pgmcp.test/.well-known/oauth-protected-resource")
	})

	t.Run("rejects a POST to /mcp with the wrong bearer token", func(t *testing.T) {
		srv := serve(t, staticOptions())

		req, err := http.NewRequest("POST", srv.URL+"/mcp", strings.NewReader(`{}`))
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer wrong-key")
		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)
		assert.NotContains(t, string(body), "wrong-key", "the presented token must never be echoed back")
	})

	t.Run("serves the whole tool catalogue to a caller holding a valid static key", func(t *testing.T) {
		srv := serve(t, staticOptions())

		names := listTools(t, srv.URL+"/mcp", "correct-horse-battery-staple")

		assert.Len(t, names, 9)
		assert.ElementsMatch(t, tool.Names(tool.Options{}), names)
	})

	t.Run("serves the tool catalogue to a caller holding a valid jwt", func(t *testing.T) {
		f := newJWKSFixture(t)
		o := staticOptions()
		o.AuthMode = "jwt"
		o.APIKeys = nil
		o.JWKSURL = f.url
		o.JWTIssuer = testIssuer
		o.JWTAudience = testAudience
		srv := serve(t, o)

		names := listTools(t, srv.URL+"/mcp", f.sign(t, validClaims()))

		assert.Len(t, names, 9)
	})

	t.Run("serves the whole tool catalogue with no token at all when auth is off", func(t *testing.T) {
		srv := serve(t, Options{
			Listen:   "127.0.0.1:0",
			AuthMode: "none",
			Log:      discardLog(),
			Health:   func(context.Context) error { return nil },
		})

		names := listTools(t, srv.URL+"/mcp", "")

		assert.Len(t, names, 9)
	})

	t.Run("issues no resource metadata challenge when no authorization server is configured", func(t *testing.T) {
		o := staticOptions()
		o.AuthServers = nil
		srv := serve(t, o)

		resp, err := http.Post(srv.URL+"/mcp", "application/json", strings.NewReader(`{}`))
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusUnauthorized, resp.StatusCode)
		assert.NotContains(t, resp.Header.Get("WWW-Authenticate"), "resource_metadata")
	})
}

func TestHandlerProtectedResourceMetadata(t *testing.T) {
	t.Run("serves the protected resource metadata document when an authorization server is configured", func(t *testing.T) {
		srv := serve(t, staticOptions())

		resp, err := http.Get(srv.URL + "/.well-known/oauth-protected-resource")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		require.Equal(t, http.StatusOK, resp.StatusCode)

		var document struct {
			Resource               string   `json:"resource"`
			AuthorizationServers   []string `json:"authorization_servers"`
			BearerMethodsSupported []string `json:"bearer_methods_supported"`
			ResourceName           string   `json:"resource_name"`
		}
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&document))

		assert.Equal(t, "https://pgmcp.test", document.Resource)
		assert.Equal(t, []string{"https://issuer.test/"}, document.AuthorizationServers)
		assert.Equal(t, []string{"header"}, document.BearerMethodsSupported)
		assert.Equal(t, "pgmcp", document.ResourceName)
	})

	t.Run("serves no metadata document when no authorization server is configured", func(t *testing.T) {
		o := staticOptions()
		o.AuthServers = nil
		srv := serve(t, o)

		resp, err := http.Get(srv.URL + "/.well-known/oauth-protected-resource")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	})
}

func TestHandlerHealth(t *testing.T) {
	t.Run("reports ok when the health check passes", func(t *testing.T) {
		o := staticOptions()
		o.Health = func(context.Context) error { return nil }
		srv := serve(t, o)

		resp, err := http.Get(srv.URL + "/healthz")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, http.StatusOK, resp.StatusCode)
		assert.Equal(t, "application/json", resp.Header.Get("Content-Type"))
		assert.JSONEq(t, `{"status":"ok"}`, string(body))
	})

	t.Run("reports degraded without leaking the failure detail when the health check fails", func(t *testing.T) {
		o := staticOptions()
		o.Health = func(context.Context) error {
			return errors.New(`dial postgres://pgmcp:hunter2@db.internal:5432/app: connection refused`)
		}
		srv := serve(t, o)

		resp, err := http.Get(srv.URL + "/healthz")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
		assert.JSONEq(t, `{"status":"degraded"}`, string(body))
		assert.NotContains(t, string(body), "hunter2")
		assert.NotContains(t, string(body), "postgres")
		assert.NotContains(t, string(body), "connection refused")
	})

	t.Run("needs no bearer token, so an orchestrator can probe an authenticated server", func(t *testing.T) {
		srv := serve(t, staticOptions())

		resp, err := http.Get(srv.URL + "/healthz")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("reports ok when no health check is configured", func(t *testing.T) {
		o := staticOptions()
		o.Health = nil
		srv := serve(t, o)

		resp, err := http.Get(srv.URL + "/healthz")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusOK, resp.StatusCode)
	})

	t.Run("answers only GET", func(t *testing.T) {
		srv := serve(t, staticOptions())

		resp, err := http.Post(srv.URL+"/healthz", "application/json", strings.NewReader(`{}`))
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		assert.Equal(t, http.StatusMethodNotAllowed, resp.StatusCode)
	})
}

func TestHandlerBodyLimit(t *testing.T) {
	t.Run("rejects a request body over the one mebibyte cap and still answers", func(t *testing.T) {
		srv := serve(t, staticOptions())

		body := bytes.NewReader(bytes.Repeat([]byte("a"), 2<<20))
		req, err := http.NewRequest("POST", srv.URL+"/mcp", body)
		require.NoError(t, err)
		req.Header.Set("Authorization", "Bearer correct-horse-battery-staple")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json, text/event-stream")

		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Do(req)
		require.NoError(t, err, "an oversized request must be answered, not left hanging")
		defer func() { _ = resp.Body.Close() }()

		assert.GreaterOrEqual(t, resp.StatusCode, 400)
		assert.Less(t, resp.StatusCode, 500)
		assert.Equal(t, http.StatusRequestEntityTooLarge, resp.StatusCode)
	})
}

func TestNewServer(t *testing.T) {
	t.Run("bounds every phase a slow or idle client can stall, and leaves streaming writes unbounded", func(t *testing.T) {
		srv := NewServer("127.0.0.1:8080", http.NotFoundHandler())

		assert.Equal(t, "127.0.0.1:8080", srv.Addr)
		assert.NotNil(t, srv.Handler)
		assert.Equal(t, 10*time.Second, srv.ReadHeaderTimeout)
		assert.Equal(t, 30*time.Second, srv.ReadTimeout)
		assert.Zero(t, srv.WriteTimeout, "streamable responses are long-lived, so writes must not be bounded")
		assert.Equal(t, 120*time.Second, srv.IdleTimeout)
	})
}
