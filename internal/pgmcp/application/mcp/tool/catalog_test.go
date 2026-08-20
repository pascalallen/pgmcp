package tool

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// serveCatalog registers the whole catalogue on a fresh in-memory server and
// returns a client session connected to it.
func serveCatalog(t *testing.T, deps Deps, opts Options) *mcp.ClientSession {
	t.Helper()

	ctx := context.Background()

	server := mcp.NewServer(&mcp.Implementation{Name: "pgmcp-test", Version: "v0.0.1"}, nil)
	Register(server, deps, opts)

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

// listedNames returns the tool names the server advertises, in the order it
// advertises them.
func listedNames(t *testing.T, session *mcp.ClientSession) []string {
	t.Helper()

	listed, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
	require.NoError(t, err)

	names := make([]string, 0, len(listed.Tools))
	for _, definition := range listed.Tools {
		names = append(names, definition.Name)
	}

	return names
}

func TestRegister(t *testing.T) {
	deps := Deps{Diag: fakeDiag{}, Parser: selectParser()}

	t.Run("the catalogue registers all nine tools by default", func(t *testing.T) {
		session := serveCatalog(t, deps, Options{})

		names := listedNames(t, session)
		assert.Len(t, names, 9)
		assert.ElementsMatch(t, []string{
			"top_queries", "explain", "index_health", "table_health",
			"lock_waits", "connections", "replication", "config_check", "query",
		}, names)
	})

	t.Run("the catalogue registers eight tools when the query tool is disabled", func(t *testing.T) {
		session := serveCatalog(t, deps, Options{DisableQuery: true})

		names := listedNames(t, session)
		assert.Len(t, names, 8)
		assert.NotContains(t, names, "query")
	})

	t.Run("a disabled query tool cannot be called at all", func(t *testing.T) {
		session := serveCatalog(t, deps, Options{DisableQuery: true})

		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "query",
			Arguments: QueryIn{SQL: "SELECT 1"},
		})
		if err == nil {
			require.NotNil(t, result)
			assert.True(t, result.IsError, "an unregistered tool must not answer")
		}
	})

	t.Run("the catalogue advertises exactly the tools Names reports", func(t *testing.T) {
		for _, opts := range []Options{{}, {DisableQuery: true}} {
			session := serveCatalog(t, deps, opts)
			assert.ElementsMatch(t, Names(opts), listedNames(t, session))
		}
	})

	t.Run("the listing is the same on every server the catalogue builds", func(t *testing.T) {
		first := listedNames(t, serveCatalog(t, deps, Options{}))
		second := listedNames(t, serveCatalog(t, deps, Options{}))

		assert.Equal(t, first, second, "two servers must advertise the same tools in the same order")
		assert.IsNonDecreasing(t, first, "the sdk sorts its listing by name")
	})

	t.Run("every registered tool is annotated read only with a title and an output schema", func(t *testing.T) {
		session := serveCatalog(t, deps, Options{})

		listed, err := session.ListTools(context.Background(), &mcp.ListToolsParams{})
		require.NoError(t, err)

		for _, definition := range listed.Tools {
			t.Run(definition.Name, func(t *testing.T) {
				assert.NotEmpty(t, definition.Description)
				require.NotNil(t, definition.Annotations)
				assert.True(t, definition.Annotations.ReadOnlyHint)
				assert.True(t, definition.Annotations.IdempotentHint)
				assert.NotEmpty(t, definition.Annotations.Title)
				require.NotNil(t, definition.Annotations.DestructiveHint)
				assert.False(t, *definition.Annotations.DestructiveHint)
				require.NotNil(t, definition.Annotations.OpenWorldHint)
				assert.False(t, *definition.Annotations.OpenWorldHint)
				assert.NotNil(t, definition.OutputSchema)
			})
		}
	})

	t.Run("the catalogue hands the query tool its schema allowlist", func(t *testing.T) {
		session := serveCatalog(t, Deps{Diag: fakeDiag{}, Parser: schemaParser("private")}, Options{QuerySchemas: []string{"public"}})

		result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "query",
			Arguments: QueryIn{SQL: "SELECT * FROM private.secrets"},
		})
		require.NoError(t, err)
		assert.True(t, result.IsError)
		assert.Contains(t, contentText(result), `schema "private" is not in the allowed list`)
	})

	t.Run("the catalogue logs what it registered when given a logger", func(t *testing.T) {
		var logged bytes.Buffer
		logger := slog.New(slog.NewTextHandler(&logged, &slog.HandlerOptions{Level: slog.LevelDebug}))

		serveCatalog(t, Deps{Diag: fakeDiag{}, Parser: selectParser(), Log: logger}, Options{})

		assert.Contains(t, logged.String(), "registered mcp tools")
		assert.Contains(t, logged.String(), "top_queries")
	})
}

func TestNames(t *testing.T) {
	t.Run("names lists nine tools by default and eight without the query tool", func(t *testing.T) {
		assert.Len(t, Names(Options{}), 9)
		assert.Len(t, Names(Options{DisableQuery: true}), 8)
	})

	t.Run("names ends with the query tool and drops it when disabled", func(t *testing.T) {
		full := Names(Options{})
		assert.Equal(t, queryToolName, full[len(full)-1])
		assert.NotContains(t, Names(Options{DisableQuery: true}), queryToolName)
	})

	t.Run("names returns a fresh slice the caller cannot use to mutate the catalogue", func(t *testing.T) {
		names := Names(Options{})
		names[0] = "tampered"

		assert.Equal(t, "top_queries", Names(Options{})[0])
	})
}
