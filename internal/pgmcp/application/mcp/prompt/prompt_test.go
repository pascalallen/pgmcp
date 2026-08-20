package prompt_test

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/application/mcp/prompt"
)

// connect wires a server with diagnose_slow_query registered to an
// in-memory client session, cleaning up both when the test ends.
func connect(t *testing.T) *mcp.ClientSession {
	t.Helper()

	s := mcp.NewServer(&mcp.Implementation{Name: "pgmcp-test", Version: "0.0.0"}, nil)
	prompt.Register(s)

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	ctx := context.Background()

	serverSession, err := s.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "pgmcp-client-test", Version: "0.0.0"}, nil)

	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })

	return clientSession
}

func TestPromptRegister(t *testing.T) {
	ctx := context.Background()

	t.Run("prompt lists one required argument and renders the sql into the message", func(t *testing.T) {
		cs := connect(t)

		listed, err := cs.ListPrompts(ctx, nil)
		require.NoError(t, err)
		require.Len(t, listed.Prompts, 1)

		p := listed.Prompts[0]
		assert.Equal(t, "diagnose_slow_query", p.Name)
		require.Len(t, p.Arguments, 1)
		assert.Equal(t, "sql", p.Arguments[0].Name)
		assert.True(t, p.Arguments[0].Required)

		const sql = "SELECT * FROM students WHERE school_id = $1"

		got, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{
			Name:      "diagnose_slow_query",
			Arguments: map[string]string{"sql": sql},
		})
		require.NoError(t, err)
		require.Len(t, got.Messages, 1)

		msg := got.Messages[0]
		assert.Equal(t, mcp.Role("user"), msg.Role)

		text, ok := msg.Content.(*mcp.TextContent)
		require.True(t, ok, "expected a *mcp.TextContent message")
		assert.Contains(t, text.Text, sql)
		assert.Contains(t, text.Text, "explain")
		assert.Contains(t, text.Text, "analyze")
		assert.Contains(t, text.Text, "hot_nodes")
		assert.Contains(t, text.Text, "index_health")
		assert.Contains(t, text.Text, "table_health")
		assert.Contains(t, text.Text, "top_queries")
		assert.Contains(t, text.Text, "mean_time")
		assert.Contains(t, text.Text, "Never run DDL")
	})

	t.Run("get prompt without sql errors", func(t *testing.T) {
		cs := connect(t)

		_, err := cs.GetPrompt(ctx, &mcp.GetPromptParams{Name: "diagnose_slow_query"})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing required argument: sql")
	})
}
