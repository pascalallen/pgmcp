package diagnostics

import "sort"

// FindCycles returns the deadlock cycles in the blocked → blocking wait graph.
// Each cycle is the set of PIDs of one strongly connected component of size two
// or more, sorted ascending; the cycles themselves are sorted lexicographically
// so the result is stable. Self-blocking edges cannot deadlock and are ignored.
func FindCycles(edges []LockEdge) [][]int {
	adjacency := make(map[int][]int, len(edges))
	seen := make(map[[2]int]bool, len(edges))
	for _, edge := range edges {
		if edge.BlockedPID == edge.BlockingPID {
			continue
		}
		pair := [2]int{edge.BlockedPID, edge.BlockingPID}
		if seen[pair] {
			continue
		}
		seen[pair] = true
		adjacency[edge.BlockedPID] = append(adjacency[edge.BlockedPID], edge.BlockingPID)
		if _, ok := adjacency[edge.BlockingPID]; !ok {
			adjacency[edge.BlockingPID] = nil
		}
	}

	cycles := [][]int{}
	for _, component := range stronglyConnected(adjacency) {
		if len(component) < 2 {
			continue
		}
		sort.Ints(component)
		cycles = append(cycles, component)
	}
	sort.Slice(cycles, func(i, j int) bool { return lessPIDs(cycles[i], cycles[j]) })

	return cycles
}

// tarjan carries the mutable state of one strongly-connected-component search.
type tarjan struct {
	adjacency  map[int][]int
	index      map[int]int
	lowLink    map[int]int
	onStack    map[int]bool
	stack      []int
	next       int
	components [][]int
}

// stronglyConnected returns the strongly connected components of the graph.
func stronglyConnected(adjacency map[int][]int) [][]int {
	state := &tarjan{
		adjacency: adjacency,
		index:     make(map[int]int, len(adjacency)),
		lowLink:   make(map[int]int, len(adjacency)),
		onStack:   make(map[int]bool, len(adjacency)),
	}

	nodes := make([]int, 0, len(adjacency))
	for node := range adjacency {
		nodes = append(nodes, node)
	}
	sort.Ints(nodes)

	for _, node := range nodes {
		if _, visited := state.index[node]; !visited {
			state.visit(node)
		}
	}

	return state.components
}

// visit performs one step of Tarjan's algorithm from node.
func (t *tarjan) visit(node int) {
	t.index[node] = t.next
	t.lowLink[node] = t.next
	t.next++
	t.stack = append(t.stack, node)
	t.onStack[node] = true

	neighbours := append([]int(nil), t.adjacency[node]...)
	sort.Ints(neighbours)
	for _, neighbour := range neighbours {
		if _, visited := t.index[neighbour]; !visited {
			t.visit(neighbour)
			t.lowLink[node] = min(t.lowLink[node], t.lowLink[neighbour])

			continue
		}
		if t.onStack[neighbour] {
			t.lowLink[node] = min(t.lowLink[node], t.index[neighbour])
		}
	}

	if t.lowLink[node] != t.index[node] {
		return
	}

	var component []int
	for {
		last := len(t.stack) - 1
		member := t.stack[last]
		t.stack = t.stack[:last]
		t.onStack[member] = false
		component = append(component, member)
		if member == node {
			break
		}
	}
	t.components = append(t.components, component)
}

// lessPIDs orders two PID lists lexicographically.
func lessPIDs(a, b []int) bool {
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}

	return len(a) < len(b)
}
