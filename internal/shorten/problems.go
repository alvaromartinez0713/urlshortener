package shorten

import (
	"errors"
	"net/http"

	"urlshortener/internal/platform/httpx"
	"urlshortener/internal/shorturl"
)

// problems.go owns THE single domain-error → HTTP mapping of the shorten
// slice (S9, S17): it is deliberately separated from the handler, whose
// only error job is attaching (S13). It lives per slice by design (S5.4):
// the errors it inspects are shorten-slice/domain values; redirect and
// future slices own their own mappings, while httpx owns the rendering
// and the canonical generic problem (S17).
//
// Every mapping returns an httpx.ProblemError (S13): the Problem is
// user-safe and gets rendered by the error middleware; the original error
// travels as the cause and reaches only the logs, never the user.

// isInvalidInput reports whether err is a client-input error: the domain
// validation errors surfaced by the use case. Scheme rejection is a
// distinct domain value but still invalid input.
func isInvalidInput(err error) bool {
	return errors.Is(err, shorturl.ErrEmptyURL) ||
		errors.Is(err, shorturl.ErrInvalidURL) ||
		errors.Is(err, shorturl.ErrInvalidURLScheme)
}

func problemFor(err error) *httpx.ProblemError {
	switch {
	case isInvalidInput(err):
		// S25: the detail is a FIXED user-safe message — the offending
		// raw input must never be echoed back (it travels in the cause,
		// for the logs only).
		return httpx.NewProblemError(httpx.Problem{
			Status: http.StatusUnprocessableEntity,
			Type:   httpx.TypeURI("invalid-url"),
			Detail: "the provided URL is not a valid absolute http/https URL",
		}, err)
	case errors.Is(err, ErrCodeGenerationExhausted):
		return httpx.NewProblemError(httpx.Problem{
			Status: http.StatusInternalServerError,
			Type:   httpx.TypeURI("code-generation-exhausted"),
			Detail: "the short code could not be generated at this time",
		}, err)
	default:
		// Infrastructure failure: the canonical generic treatment (S17),
		// no internals leaked (S8).
		return httpx.NewProblemError(httpx.GenericInternalProblem(), err)
	}
}
