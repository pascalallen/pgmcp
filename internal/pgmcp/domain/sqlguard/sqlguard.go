// Package sqlguard enforces that every SQL statement the server executes is
// read-only, on top of running inside a READ ONLY transaction. A read-only
// transaction does not stop calls like pg_terminate_backend, pg_read_file,
// pg_sleep, advisory locks, or setval/nextval, so Validate additionally
// inspects the parsed statement: it must be a single top-level
// SELECT/EXPLAIN/SHOW with no nested write statement, no locking clause
// (FOR UPDATE/SHARE), no SELECT INTO, and no call to a denied function.
package sqlguard

import (
	"fmt"
	"strings"
)

// Statement is the parser-neutral summary of a parsed SQL string, filled in
// by the infrastructure/postgres.Parser adapter.
type Statement struct {
	Kinds     []string        // top-level statement node names, e.g. "SelectStmt", "UpdateStmt"
	NodeTypes map[string]bool // every node-type key seen anywhere in the tree ("LockingClause", "IntoClause", "DeleteStmt", …)
	Functions []string        // every called function name, lowercase, last path segment ("pg_sleep", "now")
	Schemas   []string        // every schema qualifying a table reference, as parsed; "" for an unqualified reference
}

// Parser parses a raw SQL string into a Statement summary.
type Parser interface {
	Parse(sql string) (*Statement, error)
}

// Reason identifies why Validate rejected a statement.
type Reason string

const (
	ReasonParse      Reason = "parse_error"
	ReasonMultiple   Reason = "multiple_statements"
	ReasonKind       Reason = "statement_kind_not_allowed"
	ReasonNestedStmt Reason = "nested_statement_not_allowed"
	ReasonLocking    Reason = "locking_clause_not_allowed"
	ReasonInto       Reason = "select_into_not_allowed"
	ReasonFunction   Reason = "function_not_allowed"
)

// Rejection is the error Validate returns for a disallowed statement.
// Callers should use errors.As to recover it from the error Validate
// returns.
type Rejection struct {
	Reason Reason
	Detail string
}

// Error implements the error interface.
func (r *Rejection) Error() string {
	return fmt.Sprintf("sqlguard: %s: %s", r.Reason, r.Detail)
}

// AllowedKinds are the only top-level statement kinds Validate permits.
var AllowedKinds = map[string]bool{
	"SelectStmt":       true,
	"ExplainStmt":      true,
	"VariableShowStmt": true,
}

// DeniedFunctions are exact, lowercase function names Validate rejects even
// inside an otherwise-allowed SELECT/EXPLAIN/SHOW — each can mutate state or
// affect other sessions from inside a read-only transaction.
var DeniedFunctions = []string{
	"pg_terminate_backend", "pg_cancel_backend", "pg_reload_conf", "pg_rotate_logfile",
	"pg_switch_wal", "pg_create_restore_point", "pg_backup_start", "pg_backup_stop",
	"pg_start_backup", "pg_stop_backup", "pg_promote", "pg_read_file", "pg_read_binary_file",
	"pg_stat_file", "pg_sleep", "pg_sleep_for", "pg_sleep_until", "pg_notify", "set_config",
	"setval", "nextval", "lo_import", "lo_export", "lo_unlink", "lo_create", "lo_creat",
	"lo_from_bytea", "lo_put", "lo_truncate", "lo_open", "brin_summarize_new_values",
	"brin_summarize_range", "brin_desummarize_range", "gin_clean_pending_list",
	"pg_wal_replay_pause", "pg_wal_replay_resume", "pg_log_backend_memory_contexts",
	"pg_stat_reset", "pg_stat_reset_shared", "pg_stat_reset_single_table_counters",
	"pg_stat_reset_single_function_counters", "pg_stat_statements_reset",
}

// DeniedFunctionPrefixes are lowercase function-name prefixes Validate
// rejects, covering whole families of functions rather than single names.
var DeniedFunctionPrefixes = []string{
	"dblink", "pg_advisory", "pg_try_advisory", "pg_ls_", "pg_file_", "pg_logical_",
	"pg_replication_origin_", "pg_create_logical_replication_slot",
	"pg_create_physical_replication_slot", "pg_drop_replication_slot", "pg_copy_",
}

// Validate parses sql with p and rejects anything that is not a single,
// read-only SELECT/EXPLAIN/SHOW statement. It returns nil when sql is
// allowed, and a *Rejection otherwise — callers should use errors.As to
// inspect the reason.
func Validate(sql string, p Parser) error {
	stmt, err := p.Parse(sql)
	if err != nil {
		return &Rejection{Reason: ReasonParse, Detail: err.Error()}
	}

	if len(stmt.Kinds) != 1 {
		return &Rejection{Reason: ReasonMultiple, Detail: fmt.Sprintf("%d statements", len(stmt.Kinds))}
	}

	kind := stmt.Kinds[0]
	if !AllowedKinds[kind] {
		return &Rejection{Reason: ReasonKind, Detail: kind}
	}

	for nodeType := range stmt.NodeTypes {
		if strings.HasSuffix(nodeType, "Stmt") && !AllowedKinds[nodeType] {
			return &Rejection{Reason: ReasonNestedStmt, Detail: nodeType}
		}
	}

	if stmt.NodeTypes["LockingClause"] {
		return &Rejection{Reason: ReasonLocking, Detail: "FOR UPDATE/SHARE locking clause"}
	}

	if stmt.NodeTypes["IntoClause"] {
		return &Rejection{Reason: ReasonInto, Detail: "SELECT INTO"}
	}

	for _, fn := range stmt.Functions {
		name := strings.ToLower(fn)
		if isDeniedFunction(name) {
			return &Rejection{Reason: ReasonFunction, Detail: name}
		}
	}

	return nil
}

// isDeniedFunction reports whether name (already lowercase) is on the
// denylist, either by exact match or by prefix.
func isDeniedFunction(name string) bool {
	for _, denied := range DeniedFunctions {
		if name == denied {
			return true
		}
	}
	for _, prefix := range DeniedFunctionPrefixes {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}
