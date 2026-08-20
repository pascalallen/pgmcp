// Package tool exposes the read-only diagnostics port as MCP tools. Every
// tool takes a typed input, returns a typed output that embeds Meta, carries
// read-only annotations, and never mutates the database.
package tool

import (
	"context"
	"time"

	"github.com/pascalallen/pgmcp/internal/pgmcp/domain/diagnostics"
)

// Meta is the provenance block every tool output embeds, so a model reading a
// result always knows which server and which moment it describes.
type Meta = diagnostics.Meta

// newMeta stamps the current time and asks the port which server answered.
// The lookup is one cheap SELECT per call and is deliberately best effort: a
// diagnostics tool that has already produced its answer must not fail because
// the version probe did, so a failed lookup yields a Meta carrying only
// GeneratedAt.
func newMeta(ctx context.Context, d diagnostics.Diagnostics) Meta {
	meta := Meta{GeneratedAt: time.Now().UTC()}

	info, err := d.ServerInfo(ctx)
	if err != nil || info == nil {
		return meta
	}

	meta.ServerVersion = info.Version
	meta.Database = info.Database

	return meta
}
