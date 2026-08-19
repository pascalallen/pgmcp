package tool

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/sqlguard"
)

// errNotStubbed is what a fake port method returns when a test did not stub
// it, so an unexpected call fails loudly instead of returning a zero value.
var errNotStubbed = errors.New("fake diagnostics: method not stubbed")

// fakeDiag is a Diagnostics port whose every method is a function field, so a
// test stubs only the methods it exercises. The embedded interface keeps the
// fake satisfying the port even if the port grows a method.
type fakeDiag struct {
	diagnostics.Diagnostics

	serverInfo  func(ctx context.Context) (*diagnostics.ServerInfo, error)
	overview    func(ctx context.Context) (*diagnostics.Overview, error)
	settings    func(ctx context.Context) ([]diagnostics.Setting, error)
	topQueries  func(ctx context.Context, p diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error)
	explain     func(ctx context.Context, p diagnostics.ExplainParams) (*diagnostics.ExplainResult, error)
	lockWaits   func(ctx context.Context, p diagnostics.LockWaitsParams) (*diagnostics.LockWaitsResult, error)
	indexHealth func(ctx context.Context, p diagnostics.IndexHealthParams) (*diagnostics.IndexHealthResult, error)
	tableHealth func(ctx context.Context, p diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error)
	connections func(ctx context.Context, p diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error)
	replication func(ctx context.Context) (*diagnostics.ReplicationResult, error)
	query       func(ctx context.Context, p diagnostics.QueryParams) (*diagnostics.QueryResult, error)
}

// ServerInfo implements the Diagnostics port.
func (f fakeDiag) ServerInfo(ctx context.Context) (*diagnostics.ServerInfo, error) {
	if f.serverInfo == nil {
		return &diagnostics.ServerInfo{Version: "16.4", Database: "pgmcp"}, nil
	}

	return f.serverInfo(ctx)
}

// Overview implements the Diagnostics port.
func (f fakeDiag) Overview(ctx context.Context) (*diagnostics.Overview, error) {
	if f.overview == nil {
		return nil, errNotStubbed
	}

	return f.overview(ctx)
}

// Settings implements the Diagnostics port.
func (f fakeDiag) Settings(ctx context.Context) ([]diagnostics.Setting, error) {
	if f.settings == nil {
		return nil, errNotStubbed
	}

	return f.settings(ctx)
}

// TopQueries implements the Diagnostics port.
func (f fakeDiag) TopQueries(ctx context.Context, p diagnostics.TopQueriesParams) (*diagnostics.TopQueriesResult, error) {
	if f.topQueries == nil {
		return nil, errNotStubbed
	}

	return f.topQueries(ctx, p)
}

// Explain implements the Diagnostics port.
func (f fakeDiag) Explain(ctx context.Context, p diagnostics.ExplainParams) (*diagnostics.ExplainResult, error) {
	if f.explain == nil {
		return nil, errNotStubbed
	}

	return f.explain(ctx, p)
}

// LockWaits implements the Diagnostics port.
func (f fakeDiag) LockWaits(ctx context.Context, p diagnostics.LockWaitsParams) (*diagnostics.LockWaitsResult, error) {
	if f.lockWaits == nil {
		return nil, errNotStubbed
	}

	return f.lockWaits(ctx, p)
}

// IndexHealth implements the Diagnostics port.
func (f fakeDiag) IndexHealth(ctx context.Context, p diagnostics.IndexHealthParams) (*diagnostics.IndexHealthResult, error) {
	if f.indexHealth == nil {
		return nil, errNotStubbed
	}

	return f.indexHealth(ctx, p)
}

// TableHealth implements the Diagnostics port.
func (f fakeDiag) TableHealth(ctx context.Context, p diagnostics.TableHealthParams) ([]diagnostics.TableFinding, error) {
	if f.tableHealth == nil {
		return nil, errNotStubbed
	}

	return f.tableHealth(ctx, p)
}

// Connections implements the Diagnostics port.
func (f fakeDiag) Connections(ctx context.Context, p diagnostics.ConnectionsParams) (*diagnostics.ConnectionsResult, error) {
	if f.connections == nil {
		return nil, errNotStubbed
	}

	return f.connections(ctx, p)
}

// Replication implements the Diagnostics port.
func (f fakeDiag) Replication(ctx context.Context) (*diagnostics.ReplicationResult, error) {
	if f.replication == nil {
		return nil, errNotStubbed
	}

	return f.replication(ctx)
}

// Query implements the Diagnostics port.
func (f fakeDiag) Query(ctx context.Context, p diagnostics.QueryParams) (*diagnostics.QueryResult, error) {
	if f.query == nil {
		return nil, errNotStubbed
	}

	return f.query(ctx, p)
}

// fakeParser is a sqlguard.Parser returning a canned statement summary, so
// application tests never reach for the infrastructure parser.
type fakeParser struct {
	statement *sqlguard.Statement
	err       error
}

// Parse implements sqlguard.Parser.
func (p fakeParser) Parse(string) (*sqlguard.Statement, error) {
	if p.err != nil {
		return nil, p.err
	}

	return p.statement, nil
}

// selectParser reports every statement as a plain SELECT.
func selectParser() fakeParser {
	return fakeParser{statement: &sqlguard.Statement{Kinds: []string{"SelectStmt"}, NodeTypes: map[string]bool{}}}
}

// serveTool starts an in-memory MCP server exposing exactly one tool and
// returns a client session connected to it. Both sessions are torn down when
// the test ends.
func serveTool[In, Out any](t *testing.T, definition *mcp.Tool, handler mcp.ToolHandlerFor[In, Out]) *mcp.ClientSession {
	t.Helper()

	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "pgmcp-test", Version: "v0.0.1"}, nil)
	mcp.AddTool(server, definition, handler)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Wait() })

	client := mcp.NewClient(&mcp.Implementation{Name: "pgmcp-test-client", Version: "v0.0.1"}, nil)

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession
}

// callStructured calls a tool over the session and decodes its structured
// content into Out, failing the test if the call reported a tool error.
func callStructured[Out any](t *testing.T, session *mcp.ClientSession, name string, arguments any) Out {
	t.Helper()

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	require.NoError(t, err)
	require.False(t, result.IsError, "expected a successful call, got %s", contentText(result))
	require.NotNil(t, result.StructuredContent)

	raw, err := json.Marshal(result.StructuredContent)
	require.NoError(t, err)

	var out Out
	require.NoError(t, json.Unmarshal(raw, &out))

	return out
}

// requireReadOnlyListing asserts the server advertises the tool as read-only,
// under the given title, with an output schema clients can validate against.
func requireReadOnlyListing(t *testing.T, session *mcp.ClientSession, name, title string) {
	t.Helper()

	listed, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	require.NoError(t, err)
	require.Len(t, listed.Tools, 1)

	definition := listed.Tools[0]
	assert.Equal(t, name, definition.Name)
	assert.NotEmpty(t, definition.Description)
	require.NotNil(t, definition.Annotations)
	assert.True(t, definition.Annotations.ReadOnlyHint)
	assert.Equal(t, title, definition.Annotations.Title)
	assert.True(t, definition.Annotations.IdempotentHint)
	require.NotNil(t, definition.Annotations.DestructiveHint)
	assert.False(t, *definition.Annotations.DestructiveHint)
	require.NotNil(t, definition.Annotations.OpenWorldHint)
	assert.False(t, *definition.Annotations.OpenWorldHint)
	assert.NotNil(t, definition.OutputSchema)
}

// contentText joins the text blocks of a call result, for failure messages.
func contentText(result *mcp.CallToolResult) string {
	text := ""
	for _, content := range result.Content {
		if block, ok := content.(*mcp.TextContent); ok {
			text += block.Text
		}
	}

	return text
}
