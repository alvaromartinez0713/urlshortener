package redirect

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"urlshortener/internal/platform/httpx"
)

// Handler is the HTTP adapter of the redirect slice (S5.3): it translates
// HTTP requests into use case calls and use case results/errors into HTTP
// responses. It contains no business logic — the possibility check and
// error semantics live in the use case.
type Handler struct {
	uc *UseCase
}

// NewHandler constructs the handler over its use case.
func NewHandler(uc *UseCase) *Handler {
	return &Handler{uc: uc}
}

// Redirect handles GET /:code (S2):
//
//   - 301 with Location set to the stored target on success;
//   - 404 RFC 9457 problem details for an unknown code (S2/S8);
//   - 500 problem details for infrastructure failures — internal error
//     text never reaches the user (S8).
//
// S13: the handler renders NO error responses. Failures are attached via
// c.Error() as an httpx.ProblemError and the handler returns; the error
// middleware (internal/api) is the single place that renders the problem
// and logs the failure with the full cause chain. The 301 below is the
// only response a handler writes.
//
// 301 vs 302, documented: S2 mandates a permanent redirect. 301 lets
// browsers and proxies cache the mapping, which is correct for v1 because
// codes are immutable — there is no API to re-point a code, and the S3
// design never mutates one. If codes ever become mutable, this must be
// revisited (a cached 301 hides re-pointings from clients that saw it);
// that decision belongs to the v1 spec, not to a runtime toggle.
//
// Location exactness: gin's c.Redirect delegates to net/http.Redirect,
// which hex-escapes only non-ASCII bytes of the target. The stored target
// was already validated by the shorten slice's domain (absolute http/https
// with a host); a pure-ASCII target appears in the Location header exactly
// as stored, pinned by TestRedirectHandlerMovedPermanentlyWithExactLocation.
func (h *Handler) Redirect(c *gin.Context) {
	su, err := h.uc.Redirect(c.Request.Context(), c.Param("code"))
	if err != nil {
		_ = c.Error(problemFor(err))
		return
	}

	c.Redirect(http.StatusMovedPermanently, su.Target())
}

// problemFor is THE single domain-error → HTTP mapping of the redirect
// slice (S9). Per-slice by design (S5.4): ErrNotFound is a redirect-slice
// value; the shared httpx package renders, it never imports slice errors.
// It returns an httpx.ProblemError (S13): the Problem is user-safe and is
// rendered by the error middleware; the original error travels as the
// cause and reaches only the logs, never the user.
func problemFor(err error) *httpx.ProblemError {
	switch {
	case errors.Is(err, ErrNotFound):
		return httpx.NewProblemError(httpx.Problem{
			Status: http.StatusNotFound,
			Type:   httpx.TypeURI("unknown-code"),
			Detail: "the requested short code does not exist",
		}, err)
	default:
		// Infrastructure failure: generic treatment, no internals
		// leaked (S8).
		return httpx.NewProblemError(httpx.Problem{
			Status: http.StatusInternalServerError,
			Type:   httpx.TypeURI("internal-error"),
			Detail: "the request could not be processed at this time",
		}, err)
	}
}
