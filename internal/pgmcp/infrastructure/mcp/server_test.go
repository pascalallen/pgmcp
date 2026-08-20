package mcpserver

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/application/mcp/tool"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/sqlguard"
)

// errStub is what the stub port answers with when a test drives a tool.
var errStub = errors.New("stub diagnostics")

// stubDiagnostics satisfies the read-only diagnostics port without a database.
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

// discardLog is a logger whose output goes nowhere.
func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// connect builds a server under p and returns a live client session over an
// in-memory transport, so the assertions read the real protocol surface.
func connect(t *testing.T, p Params) *mcp.ClientSession {
	t.Helper()

	if p.Diag == nil {
		p.Diag = stubDiagnostics{}
	}
	if p.Parser == nil {
		p.Parser = stubParser{}
	}
	if p.Log == nil {
		p.Log = discardLog()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := New(p).Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	clientSession, err := mcp.NewClient(&mcp.Implementation{Name: "pgmcp-test-client", Version: "test"}, nil).
		Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession
}

// defaultParams are the parameters of a stdio server with the whole catalogue.
func defaultParams() Params {
	return Params{
		Version:         "1.2.3",
		CallTimeout:     5 * time.Second,
		RateLimitPerMin: 60,
		MaxOutputBytes:  1 << 20,
		Tools:           tool.Options{},
	}
}

func TestNew(t *testing.T) {
	ctx := context.Background()

	t.Run("identifies itself by name, title, version and website", func(t *testing.T) {
		session := connect(t, defaultParams())

		info := session.InitializeResult().ServerInfo

		require.NotNil(t, info)
		assert.Equal(t, "pgmcp", info.Name)
		assert.Equal(t, "pgmcp — Postgres ops/DBA diagnostics", info.Title)
		assert.Equal(t, "1.2.3", info.Version)
		assert.Equal(t, "https://github.com/pascalallen/pgmcp", info.WebsiteURL)
	})

	t.Run("tells a connecting client it is read-only and where to start", func(t *testing.T) {
		session := connect(t, defaultParams())

		instructions := session.InitializeResult().Instructions

		assert.Contains(t, instructions, "READ ONLY")
		assert.Contains(t, instructions, "pgmcp://overview")
		assert.Contains(t, instructions, "query tool")
		assert.LessOrEqual(t, len(strings.Split(strings.TrimSpace(instructions), "\n")), 3)
	})

	t.Run("registers the whole tool catalogue", func(t *testing.T) {
		session := connect(t, defaultParams())

		result, err := session.ListTools(ctx, nil)
		require.NoError(t, err)

		names := make([]string, 0, len(result.Tools))
		for _, registered := range result.Tools {
			names = append(names, registered.Name)
		}
		assert.ElementsMatch(t, tool.Names(tool.Options{}), names)
	})

	t.Run("leaves the query tool out when the operator disabled it", func(t *testing.T) {
		p := defaultParams()
		p.Tools = tool.Options{DisableQuery: true}
		session := connect(t, p)

		result, err := session.ListTools(ctx, nil)
		require.NoError(t, err)

		for _, registered := range result.Tools {
			assert.NotEqual(t, "query", registered.Name)
		}
		assert.Len(t, result.Tools, len(tool.Names(tool.Options{}))-1)
	})

	t.Run("registers the overview and settings resources and the slow query prompt", func(t *testing.T) {
		session := connect(t, defaultParams())

		resources, err := session.ListResources(ctx, nil)
		require.NoError(t, err)
		prompts, err := session.ListPrompts(ctx, nil)
		require.NoError(t, err)

		uris := make([]string, 0, len(resources.Resources))
		for _, registered := range resources.Resources {
			uris = append(uris, registered.URI)
		}
		assert.ElementsMatch(t, []string{"pgmcp://overview", "pgmcp://settings"}, uris)

		require.Len(t, prompts.Prompts, 1)
		assert.Equal(t, "diagnose_slow_query", prompts.Prompts[0].Name)
	})

	t.Run("throttles a caller over http, where callers are remote and plural", func(t *testing.T) {
		p := defaultParams()
		p.HTTP = true
		p.RateLimitPerMin = 1
		session := connect(t, p)

		first, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "replication"})
		require.NoError(t, err)
		second, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "replication"})
		require.NoError(t, err)

		assert.False(t, isRateLimited(t, first), "the first call is within the allowance")
		assert.True(t, isRateLimited(t, second), "the second call is over the allowance")
	})

	t.Run("throttles nobody over stdio, where the only caller is the operator", func(t *testing.T) {
		p := defaultParams()
		p.HTTP = false
		p.RateLimitPerMin = 1
		session := connect(t, p)

		for range 3 {
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "replication"})
			require.NoError(t, err)
			assert.False(t, isRateLimited(t, result))
		}
	})
}

// isRateLimited reports whether a tool result is the over-limit rejection
// rather than the stub port's own failure.
func isRateLimited(t *testing.T, result *mcp.CallToolResult) bool {
	t.Helper()

	require.NotNil(t, result)
	for _, content := range result.Content {
		text, ok := content.(*mcp.TextContent)
		if ok && strings.Contains(text.Text, "rate limit exceeded") {
			return true
		}
	}

	return false
}

func TestRateLimitBurst(t *testing.T) {
	t.Run("lets a caller spend a full minute's allowance in one burst", func(t *testing.T) {
		assert.Equal(t, 60, rateLimitBurst(60))
	})

	t.Run("never sizes a bucket below a single call", func(t *testing.T) {
		assert.Equal(t, 1, rateLimitBurst(0))
		assert.Equal(t, 1, rateLimitBurst(-5))
	})
}
