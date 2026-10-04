package terminalbudget

import "time"

const (
	// CallbackHTTPTimeout bounds one daemon complete/fail HTTP attempt. Terminal
	// callbacks may legitimately wait on server-side finalization, so this is
	// intentionally wider than the daemon's ordinary 30s control-plane client.
	CallbackHTTPTimeout = 60 * time.Second

	// FinalizerSafetyMargin leaves time for the server to marshal/persist the
	// finalized result and return the HTTP response before the daemon gives up
	// on the attempt.
	FinalizerSafetyMargin = 5 * time.Second

	// MaxResponseEngineHTTPTimeout is the largest inner Response Engine request
	// budget compatible with CallbackHTTPTimeout. The outer daemon request must
	// always outlive the inner finalizer request.
	MaxResponseEngineHTTPTimeout = CallbackHTTPTimeout - FinalizerSafetyMargin
)
