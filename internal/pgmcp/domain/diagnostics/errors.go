package diagnostics

import "errors"

// ErrExtensionMissing indicates a required Postgres extension is not installed.
var ErrExtensionMissing = errors.New("extension not installed")

// ReadOnlyViolation indicates an operation attempted a write against the read-only port.
type ReadOnlyViolation struct{ Msg string }

// Error implements the error interface.
func (e *ReadOnlyViolation) Error() string { return "read-only violation: " + e.Msg }
