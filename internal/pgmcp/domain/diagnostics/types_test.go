package diagnostics_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

func TestExplainResultMarshalsExpectedTopLevelKeys(t *testing.T) {
	b, err := json.Marshal(diagnostics.ExplainResult{})
	require.NoError(t, err)

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))

	for _, key := range []string{"plan", "hot_nodes", "warnings", "plan_hash"} {
		t.Run("has key "+key, func(t *testing.T) {
			require.Contains(t, m, key)
		})
	}

	require.NotContains(t, m, "Raw", "Raw must be excluded from JSON via json:\"-\"")
}

func TestIndexHealthResultMarshalsExpectedTopLevelKeys(t *testing.T) {
	b, err := json.Marshal(diagnostics.IndexHealthResult{})
	require.NoError(t, err)

	var m map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(b, &m))

	for _, key := range []string{"unused", "duplicate", "invalid", "bloated"} {
		t.Run("has key "+key, func(t *testing.T) {
			require.Contains(t, m, key)
		})
	}
}
