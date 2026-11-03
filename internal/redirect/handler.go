package redirect

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
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
	target, err := h.uc.Redirect(c.Request.Context(), c.Param("code"))
	if err != nil {
		switch {
		case errors.Is(err, ErrNotFound):
			writeProblem(c, http.StatusNotFound, "Not Found",
				"the requested short code does not exist")
		default:
			// Infrastructure failure: generic treatment, no internals
			// leaked (S8).
			writeProblem(c, http.StatusInternalServerError, "Internal Server Error",
				"the request could not be processed at this time")
		}
		return
	}

	c.Redirect(http.StatusMovedPermanently, target)
}

// writeProblem answers with an RFC 9457 problem details document (S8),
// the same shape as internal/api's NoRoute handler and the shorten
// slice's. Deliberately a local copy, not a shared package: slices are
// independent boundaries (S5.4) and a 10-line helper does not justify
// coupling them.
func writeProblem(c *gin.Context, status int, title, detail string) {
	c.AbortWithStatusJSON(status, gin.H{
		"type":   "about:blank",
		"title":  title,
		"status": status,
		"detail": detail,
	})
}
