package diagnostics

import "context"

// Diagnostics is the read-only port implemented by infrastructure/postgres.
type Diagnostics interface {
	ServerInfo(ctx context.Context) (*ServerInfo, error)
	Overview(ctx context.Context) (*Overview, error)
	Settings(ctx context.Context) ([]Setting, error)
	TopQueries(ctx context.Context, p TopQueriesParams) (*TopQueriesResult, error)
	Explain(ctx context.Context, p ExplainParams) (*ExplainResult, error)
	LockWaits(ctx context.Context, p LockWaitsParams) (*LockWaitsResult, error)
	IndexHealth(ctx context.Context, p IndexHealthParams) (*IndexHealthResult, error)
	TableHealth(ctx context.Context, p TableHealthParams) ([]TableFinding, error)
	Connections(ctx context.Context, p ConnectionsParams) (*ConnectionsResult, error)
	Replication(ctx context.Context) (*ReplicationResult, error)
	Query(ctx context.Context, p QueryParams) (*QueryResult, error)
}
