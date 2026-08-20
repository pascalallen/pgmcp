package diagnostics_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

func blocks(blocked, blocking int) diagnostics.LockEdge {
	return diagnostics.LockEdge{BlockedPID: blocked, BlockingPID: blocking, LockType: "relation", Mode: "AccessExclusiveLock"}
}

func TestFindCycles(t *testing.T) {
	t.Run("finds a 2-cycle", func(t *testing.T) {
		cycles := diagnostics.FindCycles([]diagnostics.LockEdge{blocks(20, 10), blocks(10, 20)})

		assert.Equal(t, [][]int{{10, 20}}, cycles)
	})

	t.Run("finds no cycle in a chain", func(t *testing.T) {
		cycles := diagnostics.FindCycles([]diagnostics.LockEdge{blocks(10, 20), blocks(20, 30)})

		assert.Empty(t, cycles)
		assert.NotNil(t, cycles)
	})

	t.Run("finds a 3-cycle and ignores unrelated edges", func(t *testing.T) {
		cycles := diagnostics.FindCycles([]diagnostics.LockEdge{
			blocks(30, 10),
			blocks(10, 20),
			blocks(20, 30),
			blocks(40, 50),
			blocks(50, 60),
		})

		assert.Equal(t, [][]int{{10, 20, 30}}, cycles)
	})

	t.Run("returns every disjoint cycle in a deterministic order", func(t *testing.T) {
		edges := []diagnostics.LockEdge{
			blocks(70, 80), blocks(80, 70),
			blocks(30, 10), blocks(10, 20), blocks(20, 30),
			blocks(90, 100),
		}

		first := diagnostics.FindCycles(edges)
		second := diagnostics.FindCycles(edges)

		assert.Equal(t, [][]int{{10, 20, 30}, {70, 80}}, first)
		assert.Equal(t, first, second)
	})

	t.Run("ignores a self-blocking edge", func(t *testing.T) {
		cycles := diagnostics.FindCycles([]diagnostics.LockEdge{blocks(10, 10)})

		assert.Empty(t, cycles)
	})

	t.Run("reports a duplicated edge pair once", func(t *testing.T) {
		cycles := diagnostics.FindCycles([]diagnostics.LockEdge{
			blocks(20, 10), blocks(10, 20), blocks(20, 10),
		})

		assert.Equal(t, [][]int{{10, 20}}, cycles)
	})

	t.Run("returns an empty result for no edges", func(t *testing.T) {
		cycles := diagnostics.FindCycles(nil)

		require.NotNil(t, cycles)
		assert.Empty(t, cycles)
	})
}
