package httpx

// ProblemError is an error that already carries its rendered RFC 9457
// problem (S13): slice-local problemFor mappings build it from a domain
// error and handlers attach it to the request via gin's c.Error(), so the
// error middleware is the SINGLE place that renders the response and logs
// the failure.
//
// Two-audiences contract (S13, ADR-003):
//
//   - Error() returns Problem.Detail — the USER-SAFE, already-mapped
//     message. This is the only text a rendered response can ever show;
//     it is intentionally what the error middleware logs as the mapped
//     detail for the team's triage.
//   - The internal cause (the domain/infrastructure error chain) travels
//     ONLY via Unwrap/cause, and the middleware logs it in full (method,
//     path, status, chain). It never reaches the response body: handlers
//     put user-safe text in Detail and the middleware renders Problem as-is.
type ProblemError struct {
	// Problem is the RFC 9457 document the error middleware renders.
	Problem Problem
	// cause is the underlying error (domain or infrastructure). It is
	// unwrappable (errors.Is / errors.As work through ProblemError) and
	// is logged, but never rendered to the user.
	cause error
}

// NewProblemError wraps a ready-to-render problem with its cause. cause
// may be nil (e.g. purely transport-level failures); Error() and the
// rendered problem are unaffected.
func NewProblemError(p Problem, cause error) *ProblemError {
	return &ProblemError{Problem: p, cause: cause}
}

// Error implements the error interface with the user-safe mapped detail.
// The internal chain is deliberately NOT part of this message — it lives
// in Unwrap for the error log only (S13).
func (e *ProblemError) Error() string {
	return e.Problem.Detail
}

// Unwrap exposes the underlying cause so errors.Is/errors.As and the
// middleware's chain unwrapping keep working through a ProblemError.
func (e *ProblemError) Unwrap() error {
	return e.cause
}
