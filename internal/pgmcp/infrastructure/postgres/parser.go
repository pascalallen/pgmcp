package postgres

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/sqlguard"
	pg_query "github.com/wasilibs/go-pgquery"
)

// Parser is the sqlguard.Parser adapter backed by libpg_query (compiled to
// WebAssembly, so no cgo). It parses SQL into PostgreSQL's own parse tree and
// summarises it as a sqlguard.Statement.
type Parser struct{}

var _ sqlguard.Parser = Parser{}

// nodeTypePattern matches the keys libpg_query uses to wrap a parse node,
// e.g. "SelectStmt", "FuncCall", "A_Const".
var nodeTypePattern = regexp.MustCompile(`^[A-Z][A-Za-z0-9_]*$`)

// embeddedNodeFields lists the parse-tree fields that hold a node struct
// inline instead of wrapping it in a node-type key. Without them a node like
// IntoClause — the parse-tree shape of "SELECT ... INTO" — would never be
// seen by the walk, because its only key is the lower-camel field name.
var embeddedNodeFields = map[string]string{
	"relation":         "RangeVar",
	"rel":              "RangeVar",
	"intoClause":       "IntoClause",
	"withClause":       "WithClause",
	"onConflictClause": "OnConflictClause",
	"alias":            "Alias",
}

// Parse parses sql and summarises the resulting tree: the top-level statement
// kinds in source order, every node type seen anywhere in the tree, every
// called function name lowercased and reduced to its last path segment
// (so "pg_catalog.pg_terminate_backend" is reported as
// "pg_terminate_backend"), and the schema qualifying every table reference
// (the empty string for an unqualified one).
func (Parser) Parse(sql string) (*sqlguard.Statement, error) {
	raw, err := pg_query.ParseToJSON(sql)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse sql: %w", err)
	}

	var tree map[string]any
	if err := json.Unmarshal([]byte(raw), &tree); err != nil {
		return nil, fmt.Errorf("postgres: decode parse tree: %w", err)
	}

	stmt := &sqlguard.Statement{
		Kinds:     make([]string, 0),
		NodeTypes: make(map[string]bool),
		Functions: make([]string, 0),
		Schemas:   make([]string, 0),
	}

	statements, _ := tree["stmts"].([]any)
	for _, entry := range statements {
		wrapper, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		node, ok := wrapper["stmt"].(map[string]any)
		if !ok {
			continue
		}
		if kind := soleNodeType(node); kind != "" {
			stmt.Kinds = append(stmt.Kinds, kind)
		}
	}

	walk := treeWalk{
		nodeTypes: stmt.NodeTypes,
		functions: make(map[string]bool),
		schemas:   make(map[string]bool),
	}
	walk.node(tree)

	for name := range walk.functions {
		stmt.Functions = append(stmt.Functions, name)
	}
	sort.Strings(stmt.Functions)

	for schema := range walk.schemas {
		stmt.Schemas = append(stmt.Schemas, schema)
	}
	sort.Strings(stmt.Schemas)

	return stmt, nil
}

// soleNodeType returns the node-type key of a node wrapper. Wrappers hold
// exactly one key; keys are sorted so a malformed tree still yields a stable
// answer.
func soleNodeType(wrapper map[string]any) string {
	keys := make([]string, 0, len(wrapper))
	for key := range wrapper {
		if nodeTypePattern.MatchString(key) {
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return ""
	}
	sort.Strings(keys)

	return keys[0]
}

// treeWalk accumulates what one pass over a parse tree learns: the node types
// it contains, the functions it calls, and the schemas its table references
// are qualified with.
type treeWalk struct {
	nodeTypes map[string]bool
	functions map[string]bool
	schemas   map[string]bool
}

// node records every node type, called function and table schema reachable
// from n.
func (w treeWalk) node(n any) {
	switch value := n.(type) {
	case map[string]any:
		for key, child := range value {
			nodeType := ""
			switch {
			case nodeTypePattern.MatchString(key):
				nodeType = key
				w.nodeTypes[key] = true
			default:
				if embedded, ok := embeddedNodeFields[key]; ok {
					if _, isObject := child.(map[string]any); isObject {
						nodeType = embedded
						w.nodeTypes[embedded] = true
					}
				}
			}
			if object, ok := child.(map[string]any); ok {
				switch nodeType {
				case "FuncCall":
					if name := functionName(object); name != "" {
						w.functions[name] = true
					}
				case "RangeVar":
					if schema, isTable := rangeVarSchema(object); isTable {
						w.schemas[schema] = true
					}
				}
			}
			w.node(child)
		}
	case []any:
		for _, child := range value {
			w.node(child)
		}
	}
}

// rangeVarSchema returns the schema qualifying a RangeVar table reference —
// the empty string when the reference is unqualified — and whether the node
// really is a table reference at all.
func rangeVarSchema(node map[string]any) (string, bool) {
	if _, named := node["relname"].(string); !named {
		return "", false
	}
	schema, _ := node["schemaname"].(string)

	return schema, true
}

// functionName returns the lowercased last segment of a FuncCall's funcname
// list, which libpg_query encodes as a list of String nodes.
func functionName(call map[string]any) string {
	segments, ok := call["funcname"].([]any)
	if !ok || len(segments) == 0 {
		return ""
	}

	last, ok := segments[len(segments)-1].(map[string]any)
	if !ok {
		return ""
	}
	str, ok := last["String"].(map[string]any)
	if !ok {
		return ""
	}
	name, ok := str["sval"].(string)
	if !ok {
		return ""
	}

	return strings.ToLower(name)
}
