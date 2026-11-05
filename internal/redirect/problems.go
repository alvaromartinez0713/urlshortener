package redirect

import (
	"errors"
	"net/http"

	"urlshortener/internal/platform/httpx"
)

// problems.go owns THE single domain-error → HTTP mapping of the redirect
// slice (S9, S17): deliberately separated from the handler, whose only
// error job is attaching (S13). Per slice by design (S5.4): ErrNotFound
// is a redirect-slice value; the shared httpx package renders, it never
// imports slice errors, and the canonical generic problem is httpx's (S17).
//
// Every mapping returns an httpx.ProblemError (S13): the Problem is
// user-safe and is rendered by the error middleware; the original error
// travels as the cause and reaches only the logs, never the user.
func problemFor(err error) *httpx.ProblemError {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NewProblemError(httpx.Problem{
			Status: http.StatusNotFound,
			Type:   httpx.TypeURI("unknown-code"),
			Detail: "the requested short code does not exist",
		}, err)
	default:
		// Infrastructure failure: the canonical generic treatment (S17),
		// no internals leaked (S8).
		return httpx.NewProblemError(httpx.GenericInternalProblem(), err)
	}
}
