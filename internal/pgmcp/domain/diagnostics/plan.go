package diagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strconv"
	"strings"
)

// Thresholds for the AnalyzePlan heuristics.
const (
	estimateMissFactor    = 10.0   // est vs actual ratio that counts as a miss
	estimateMissMinRows   = 100.0  // both sides must reach this many rows
	largeRelationRows     = 100000 // rows scanned (or estimated) that make a seq scan notable
	largeInnerSideRows    = 10000  // inner-side rows a nested loop repeats over
	discardedRowsFactor   = 10.0   // rows removed vs rows kept
	discardedRowsMinCount = 1000   // rows removed below this are noise
	hotNodeLimit          = 3
)

// explainEnvelope is one element of the EXPLAIN (FORMAT JSON) array.
type explainEnvelope struct {
	Plan        explainNode `json:"Plan"`
	PlanningMs  float64     `json:"Planning Time"`
	ExecutionMs float64     `json:"Execution Time"`
}

// explainNode mirrors the subset of EXPLAIN node keys the domain consumes.
type explainNode struct {
	NodeType    string        `json:"Node Type"`
	Relation    string        `json:"Relation Name"`
	Alias       string        `json:"Alias"`
	IndexName   string        `json:"Index Name"`
	JoinType    string        `json:"Join Type"`
	EstRows     float64       `json:"Plan Rows"`
	ActualRows  float64       `json:"Actual Rows"`
	Loops       float64       `json:"Actual Loops"`
	EstCost     float64       `json:"Total Cost"`
	TotalMs     float64       `json:"Actual Total Time"`
	SharedHit   int64         `json:"Shared Hit Blocks"`
	SharedRead  int64         `json:"Shared Read Blocks"`
	TempRead    int64         `json:"Temp Read Blocks"`
	TempWritten int64         `json:"Temp Written Blocks"`
	SortMethod  string        `json:"Sort Method"`
	Filter      string        `json:"Filter"`
	RowsRemoved int64         `json:"Rows Removed by Filter"`
	Plans       []explainNode `json:"Plans"`
}

// ParseExplainJSON turns the array produced by EXPLAIN (FORMAT JSON) into an
// ExplainResult with per-node self time, hot nodes, warnings and a shape hash.
func ParseExplainJSON(raw []byte) (*ExplainResult, error) {
	var envelopes []explainEnvelope
	if err := json.Unmarshal(raw, &envelopes); err != nil {
		return nil, err
	}
	if len(envelopes) == 0 {
		return nil, ErrNoPlan
	}

	envelope := envelopes[0]
	result := &ExplainResult{
		Plan:        convertNode(envelope.Plan),
		PlanningMs:  envelope.PlanningMs,
		ExecutionMs: envelope.ExecutionMs,
		Raw:         json.RawMessage(raw),
	}
	result.HotNodes, result.Warnings = AnalyzePlan(&result.Plan, result.ExecutionMs)
	result.PlanHash = HashPlan(&result.Plan)

	return result, nil
}

// convertNode maps one EXPLAIN node and its children onto the domain type.
func convertNode(n explainNode) PlanNode {
	node := PlanNode{
		NodeType:    n.NodeType,
		Relation:    n.Relation,
		Alias:       n.Alias,
		IndexName:   n.IndexName,
		JoinType:    n.JoinType,
		EstRows:     n.EstRows,
		ActualRows:  n.ActualRows,
		Loops:       int(n.Loops),
		EstCost:     n.EstCost,
		TotalMs:     n.TotalMs,
		SharedHit:   n.SharedHit,
		SharedRead:  n.SharedRead,
		TempRead:    n.TempRead,
		TempWritten: n.TempWritten,
		SortMethod:  n.SortMethod,
		Filter:      n.Filter,
		RowsRemoved: n.RowsRemoved,
		Children:    make([]PlanNode, 0, len(n.Plans)),
	}
	for _, child := range n.Plans {
		node.Children = append(node.Children, convertNode(child))
	}

	return node
}

// AnalyzePlan fills SelfMs on every node of the tree, then returns the hottest
// nodes by self time (at most three, descending) and the plan warnings in
// depth-first order. Both slices are non-nil.
func AnalyzePlan(root *PlanNode, executionMs float64) (hot []HotNode, warnings []string) {
	hot, warnings = []HotNode{}, []string{}
	if root == nil {
		return hot, warnings
	}

	fillSelfMs(root)

	var candidates []HotNode
	walkPlan(root, "0", func(node *PlanNode, path string) {
		if node.SelfMs > 0 {
			pct := 0.0
			if executionMs > 0 {
				pct = node.SelfMs / executionMs * 100
			}
			candidates = append(candidates, HotNode{
				Path:       path,
				NodeType:   node.NodeType,
				Relation:   node.Relation,
				SelfMs:     node.SelfMs,
				PctOfTotal: pct,
			})
		}
		warnings = append(warnings, nodeWarnings(node, path)...)
	})

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].SelfMs != candidates[j].SelfMs {
			return candidates[i].SelfMs > candidates[j].SelfMs
		}

		return candidates[i].Path < candidates[j].Path
	})
	if len(candidates) > hotNodeLimit {
		candidates = candidates[:hotNodeLimit]
	}
	hot = append(hot, candidates...)

	return hot, warnings
}

// fillSelfMs sets SelfMs to the node's total time minus its children's, clamped at zero.
func fillSelfMs(node *PlanNode) float64 {
	total := node.TotalMs * float64(effectiveLoops(node))

	var children float64
	for i := range node.Children {
		children += fillSelfMs(&node.Children[i])
	}

	node.SelfMs = math.Max(total-children, 0)

	return total
}

// walkPlan visits the tree depth first, passing each node its child-index path.
func walkPlan(node *PlanNode, path string, visit func(node *PlanNode, path string)) {
	visit(node, path)
	for i := range node.Children {
		walkPlan(&node.Children[i], path+"."+strconv.Itoa(i), visit)
	}
}

// nodeWarnings applies the plan heuristics to a single node.
func nodeWarnings(node *PlanNode, path string) []string {
	var warnings []string

	if factor, ok := estimateMiss(node); ok {
		warnings = append(warnings, "Row estimate off by "+strconv.Itoa(factor)+"x at "+path+" ("+describeNode(node)+")")
	}

	rows := node.ActualRows * float64(effectiveLoops(node))
	if node.NodeType == "Seq Scan" && (rows >= largeRelationRows || node.EstRows >= largeRelationRows) {
		warnings = append(warnings, "Sequential scan over large relation "+node.Relation)
	}

	if strings.Contains(node.SortMethod, "Disk") || node.TempWritten > 0 {
		warnings = append(warnings, "Sort/hash spilled to disk at "+path)
	}

	if node.NodeType == "Nested Loop" && len(node.Children) > 1 {
		inner := node.Children[1]
		if inner.ActualRows*float64(effectiveLoops(&inner)) >= largeInnerSideRows {
			warnings = append(warnings, "Nested loop with large inner side at "+path)
		}
	}

	removed := float64(node.RowsRemoved)
	if removed >= discardedRowsMinCount && removed >= discardedRowsFactor*node.ActualRows {
		warnings = append(warnings, "Filter discards most rows at "+path+" ("+node.Filter+")")
	}

	return warnings
}

// estimateMiss reports the rounded est/actual ratio when it exceeds the threshold.
func estimateMiss(node *PlanNode) (int, bool) {
	est, actual := node.EstRows, node.ActualRows
	if est < estimateMissMinRows || actual < estimateMissMinRows {
		return 0, false
	}

	ratio := math.Max(est, actual) / math.Min(est, actual)
	if ratio < estimateMissFactor {
		return 0, false
	}

	return int(math.Round(ratio)), true
}

// describeNode renders a node as "<type> on <relation>", or just its type.
func describeNode(node *PlanNode) string {
	if node.Relation == "" {
		return node.NodeType
	}

	return node.NodeType + " on " + node.Relation
}

// effectiveLoops treats a missing loop count as a single loop.
func effectiveLoops(node *PlanNode) int {
	if node.Loops < 1 {
		return 1
	}

	return node.Loops
}

// HashPlan returns the first 16 hex characters of the SHA-256 of the plan's
// shape lines, so that cost and timing changes leave the hash untouched.
func HashPlan(root *PlanNode) string {
	if root == nil {
		return ""
	}

	sum := sha256.Sum256([]byte(strings.Join(shapeLines(root), "\n")))

	return hex.EncodeToString(sum[:])[:16]
}

// shapeLines lists the depth-first "NodeType|Relation|IndexName|JoinType" lines.
func shapeLines(root *PlanNode) []string {
	lines := make([]string, 0, 8)
	walkPlan(root, "0", func(node *PlanNode, _ string) {
		lines = append(lines, strings.Join([]string{node.NodeType, node.Relation, node.IndexName, node.JoinType}, "|"))
	})

	return lines
}

// DiffPlans compares the shape of two plans, reporting the lines each side has
// that the other does not along with the cost and execution time deltas.
func DiffPlans(a, b *ExplainResult) PlanDiff {
	diff := PlanDiff{Added: []string{}, Removed: []string{}}
	if a == nil || b == nil {
		return diff
	}

	diff.Added = missingLines(&b.Plan, &a.Plan)
	diff.Removed = missingLines(&a.Plan, &b.Plan)
	diff.Same = len(diff.Added) == 0 && len(diff.Removed) == 0
	diff.CostDelta = b.Plan.EstCost - a.Plan.EstCost
	diff.TimeDeltaMs = b.ExecutionMs - a.ExecutionMs

	return diff
}

// missingLines returns the shape lines of have that other does not cover, sorted.
func missingLines(have, other *PlanNode) []string {
	remaining := make(map[string]int)
	for _, line := range shapeLines(other) {
		remaining[line]++
	}

	missing := []string{}
	for _, line := range shapeLines(have) {
		if remaining[line] > 0 {
			remaining[line]--

			continue
		}
		missing = append(missing, line)
	}
	sort.Strings(missing)

	return missing
}
