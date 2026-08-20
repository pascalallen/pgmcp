package httpserver

import (
	"context"
	"log/slog"
	"net/http"
)

// healthOK and healthDegraded are the only two bodies /healthz ever returns.
// Neither carries the underlying error: a failing check reports a DSN, a host
// name, or a credential in its message, and /healthz is unauthenticated so an
// orchestrator can probe it. The detail is logged instead.
var (
	healthOK       = []byte(`{"status":"ok"}`)
	healthDegraded = []byte(`{"status":"degraded"}`)
)

// healthHandler answers a liveness/readiness probe: 200 when check returns
// nil (or no check is configured), 503 otherwise.
func healthHandler(check func(context.Context) error, log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, body := http.StatusOK, healthOK

		if check != nil {
			if err := check(r.Context()); err != nil {
				status, body = http.StatusServiceUnavailable, healthDegraded
				if log != nil {
					log.Warn("health check failed", "error", err)
				}
			}
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write(body)
	})
}
