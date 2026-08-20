// Package diagnostics defines the read-only diagnostics domain: value types,
// parameters, and results produced by the Diagnostics port.
package diagnostics

import (
	"encoding/json"
	"time"
)

// Meta is embedded in every tool output.
type Meta struct {
	ServerVersion string    `json:"server_version"`
	Database      string    `json:"database"`
	GeneratedAt   time.Time `json:"generated_at"`
}

// ServerInfo describes the connected PostgreSQL server.
type ServerInfo struct {
	Version    string   `json:"version"`     // e.g. "16.4"
	VersionNum int      `json:"version_num"` // e.g. 160004
	Database   string   `json:"database"`
	UptimeSec  float64  `json:"uptime_s"`
	InRecovery bool     `json:"in_recovery"`
	Extensions []string `json:"extensions"` // installed extension names
	MaxConns   int      `json:"max_connections"`
}

// OrderBy selects the sort column for TopQueries.
type OrderBy string

const (
	OrderByTotalTime      OrderBy = "total_time"
	OrderByMeanTime       OrderBy = "mean_time"
	OrderByCalls          OrderBy = "calls"
	OrderByRows           OrderBy = "rows"
	OrderBySharedBlksRead OrderBy = "shared_blks_read"
)

// TopQueriesParams configures the TopQueries query.
type TopQueriesParams struct {
	OrderBy  OrderBy
	Limit    int
	MinCalls int64
	Database string
}

// StatementStat is one row from pg_stat_statements.
type StatementStat struct {
	QueryID  int64   `json:"queryid"`
	Query    string  `json:"query"` // normalized, truncated to 2000 runes
	Calls    int64   `json:"calls"`
	TotalMs  float64 `json:"total_ms"`
	MeanMs   float64 `json:"mean_ms"`
	StddevMs float64 `json:"stddev_ms"`
	Rows     int64   `json:"rows"`
	HitRatio float64 `json:"hit_ratio"` // shared_blks_hit / (hit+read), 0..1; 1 when no blocks
	TempBlks int64   `json:"temp_blks"` // temp_blks_read + temp_blks_written
}

// TopQueriesResult is the outcome of the TopQueries query.
type TopQueriesResult struct {
	Available  bool            `json:"available"`
	Hint       string          `json:"hint,omitempty"` // "CREATE EXTENSION pg_stat_statements; …" when !Available
	StatsSince *time.Time      `json:"stats_since,omitempty"`
	Statements []StatementStat `json:"statements"`
}

// ExplainParams configures the Explain query.
type ExplainParams struct {
	SQL     string
	Analyze bool
	Buffers bool
}

// PlanNode is one node in an EXPLAIN plan tree.
type PlanNode struct {
	NodeType    string     `json:"node_type"`
	Relation    string     `json:"relation,omitempty"`
	Alias       string     `json:"alias,omitempty"`
	IndexName   string     `json:"index_name,omitempty"`
	JoinType    string     `json:"join_type,omitempty"`
	EstRows     float64    `json:"est_rows"`
	ActualRows  float64    `json:"actual_rows,omitempty"`
	Loops       int        `json:"loops,omitempty"`
	EstCost     float64    `json:"est_cost"`           // Total Cost
	TotalMs     float64    `json:"total_ms,omitempty"` // Actual Total Time (per loop)
	SelfMs      float64    `json:"self_ms,omitempty"`  // TotalMs*Loops - Σ children TotalMs*Loops
	SharedHit   int64      `json:"shared_hit,omitempty"`
	SharedRead  int64      `json:"shared_read,omitempty"`
	TempRead    int64      `json:"temp_read,omitempty"`
	TempWritten int64      `json:"temp_written,omitempty"`
	SortMethod  string     `json:"sort_method,omitempty"`
	Filter      string     `json:"filter,omitempty"`
	RowsRemoved int64      `json:"rows_removed_by_filter,omitempty"`
	Children    []PlanNode `json:"children"`
}

// HotNode identifies a plan node contributing significant self time.
type HotNode struct {
	Path       string  `json:"path"`
	NodeType   string  `json:"node_type"`
	Relation   string  `json:"relation,omitempty"`
	SelfMs     float64 `json:"self_ms"`
	PctOfTotal float64 `json:"pct_of_total"`
}

// ExplainResult is the outcome of the Explain query.
type ExplainResult struct {
	Plan        PlanNode        `json:"plan"`
	PlanningMs  float64         `json:"planning_ms,omitempty"`
	ExecutionMs float64         `json:"execution_ms,omitempty"`
	HotNodes    []HotNode       `json:"hot_nodes"`
	Warnings    []string        `json:"warnings"`
	PlanHash    string          `json:"plan_hash"` // sha256 hex (first 16) of the structural shape (node types+relations+indexes, no numbers)
	Raw         json.RawMessage `json:"-"`
}

// PlanDiff compares two ExplainResult plans.
type PlanDiff struct {
	Same        bool     `json:"same"`
	Added       []string `json:"added"`
	Removed     []string `json:"removed"`
	CostDelta   float64  `json:"cost_delta"`
	TimeDeltaMs float64  `json:"time_delta_ms,omitempty"`
}

// LockWaitsParams configures the LockWaits query.
type LockWaitsParams struct{ MinWaitMs int64 }

// LockEdge is one blocked/blocking pair in the lock wait graph.
type LockEdge struct {
	BlockedPID    int    `json:"blocked_pid"`
	BlockedQuery  string `json:"blocked_query"`
	BlockedUser   string `json:"blocked_user"`
	BlockingPID   int    `json:"blocking_pid"`
	BlockingQuery string `json:"blocking_query"`
	BlockingUser  string `json:"blocking_user"`
	LockType      string `json:"lock_type"`
	Mode          string `json:"mode"`
	Relation      string `json:"relation,omitempty"`
	WaitMs        int64  `json:"wait_ms"`
}

// LockWaitsResult is the outcome of the LockWaits query.
type LockWaitsResult struct {
	Edges  []LockEdge `json:"edges"`
	Cycles [][]int    `json:"cycles"`
}

// IndexHealthParams configures the IndexHealth query.
type IndexHealthParams struct {
	Schema       string
	IncludeBloat bool
}

// IndexFinding describes one index flagged by the IndexHealth query.
type IndexFinding struct {
	Schema           string  `json:"schema"`
	Table            string  `json:"table"`
	Index            string  `json:"index"`
	SizeBytes        int64   `json:"size_bytes"`
	Scans            int64   `json:"scans"`
	Definition       string  `json:"definition"`
	BloatRatio       float64 `json:"bloat_ratio,omitempty"`
	BloatBytes       int64   `json:"bloat_bytes,omitempty"`
	DuplicateOf      string  `json:"duplicate_of,omitempty"`
	DropCandidateSQL string  `json:"drop_candidate_sql,omitempty"` // text only, never executed
}

// IndexHealthResult groups IndexFinding rows by the condition that flagged them.
type IndexHealthResult struct {
	Unused    []IndexFinding `json:"unused"`
	Duplicate []IndexFinding `json:"duplicate"`
	Invalid   []IndexFinding `json:"invalid"`
	Bloated   []IndexFinding `json:"bloated"`
}

// TableHealthParams configures the TableHealth query.
type TableHealthParams struct {
	Schema    string
	MinSizeMB int64
}

// TableFinding describes one table's vacuum/bloat/scan health.
type TableFinding struct {
	Schema          string     `json:"schema"`
	Table           string     `json:"table"`
	SizeBytes       int64      `json:"size_bytes"`
	LiveTuples      int64      `json:"live"`
	DeadTuples      int64      `json:"dead"`
	DeadRatio       float64    `json:"dead_ratio"`
	LastVacuum      *time.Time `json:"last_vacuum,omitempty"`
	LastAutovacuum  *time.Time `json:"last_autovacuum,omitempty"`
	LastAnalyze     *time.Time `json:"last_analyze,omitempty"`
	LastAutoanalyze *time.Time `json:"last_autoanalyze,omitempty"`
	SeqScans        int64      `json:"seq_scans"`
	IdxScans        int64      `json:"idx_scans"`
	BloatRatio      float64    `json:"bloat_ratio,omitempty"`
	BloatBytes      int64      `json:"bloat_bytes,omitempty"`
	Flags           []string   `json:"flags"` // "high_dead_ratio", "never_vacuumed", "never_analyzed", "seq_scan_heavy", "bloated"
}

// GroupBy selects the grouping dimension for Connections.
type GroupBy string // "state" | "wait_event" | "application" | "user" | "database"

// ConnectionsParams configures the Connections query.
type ConnectionsParams struct {
	GroupBy        GroupBy
	IdleInTxMinSec int
}

// ConnGroup is a connection count for one GroupBy key.
type ConnGroup struct {
	Key   string `json:"key"`
	Count int    `json:"count"`
}

// IdleInTx describes one connection idle in a transaction.
type IdleInTx struct {
	PID         int     `json:"pid"`
	AgeSec      float64 `json:"age_s"`
	User        string  `json:"user"`
	Application string  `json:"application"`
	Query       string  `json:"query"`
}

// ConnectionsResult is the outcome of the Connections query.
type ConnectionsResult struct {
	Groups            []ConnGroup `json:"groups"`
	Total             int         `json:"total"`
	MaxConnections    int         `json:"max_connections"`
	UsedPct           float64     `json:"used_pct"`
	IdleInTransaction []IdleInTx  `json:"idle_in_transaction"`
}

// Standby describes one replication standby's lag and state.
type Standby struct {
	Client          string  `json:"client"`
	ApplicationName string  `json:"application_name"`
	State           string  `json:"state"`
	SyncState       string  `json:"sync_state"`
	SentLagBytes    int64   `json:"sent_lag_bytes"`
	WriteLagBytes   int64   `json:"write_lag_bytes"`
	FlushLagBytes   int64   `json:"flush_lag_bytes"`
	ReplayLagBytes  int64   `json:"replay_lag_bytes"`
	WriteLagMs      float64 `json:"write_lag_ms"`
	FlushLagMs      float64 `json:"flush_lag_ms"`
	ReplayLagMs     float64 `json:"replay_lag_ms"`
}

// Slot describes one replication slot.
type Slot struct {
	Name             string `json:"name"`
	Type             string `json:"type"`
	Active           bool   `json:"active"`
	RetainedWALBytes int64  `json:"retained_wal_bytes"`
}

// ReplicationResult is the outcome of the Replication query.
type ReplicationResult struct {
	IsPrimary            bool      `json:"is_primary"`
	Standbys             []Standby `json:"standbys"`
	Slots                []Slot    `json:"slots"`
	WALRateBytesPerSec   float64   `json:"wal_rate_bytes_s"`
	ReplayLagMsOnStandby float64   `json:"replay_lag_ms,omitempty"`
}

// Setting is one row from pg_settings.
type Setting struct {
	Name      string `json:"name"`
	Value     string `json:"value"`
	Unit      string `json:"unit,omitempty"`
	Source    string `json:"source"`
	Category  string `json:"category"`
	ShortDesc string `json:"short_desc"`
}

// Verdict is the assessment of a checked setting.
type Verdict string // "ok" | "review" | "warn"

// SettingCheck is a Setting annotated with a Verdict.
type SettingCheck struct {
	Name    string  `json:"name"`
	Value   string  `json:"value"`
	Unit    string  `json:"unit,omitempty"`
	Source  string  `json:"source"`
	Verdict Verdict `json:"verdict"`
	Note    string  `json:"note"`
}

// Overview is the top-level dashboard summary.
type Overview struct {
	Server           ServerInfo     `json:"server"`
	Databases        []DatabaseSize `json:"databases"`
	CacheHitRatio    float64        `json:"cache_hit_ratio"`
	Connections      int            `json:"connections"`
	MaxConnections   int            `json:"max_connections"`
	PgStatStatements bool           `json:"pg_stat_statements"`
}

// DatabaseSize is one database's on-disk size.
type DatabaseSize struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
}

// QueryParams configures the read-only ad hoc Query.
type QueryParams struct {
	SQL     string
	Params  []any
	MaxRows int
	Timeout time.Duration
}

// Column describes one result column's name and type.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
}

// QueryResult is the outcome of the ad hoc Query.
type QueryResult struct {
	Columns    []Column `json:"columns"`
	Rows       [][]any  `json:"rows"`
	RowCount   int      `json:"row_count"`
	Truncated  bool     `json:"truncated"`
	DurationMs float64  `json:"duration_ms"`
}
