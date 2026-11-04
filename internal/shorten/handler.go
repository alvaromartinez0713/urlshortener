package shorten

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"urlshortener/internal/platform/httpx"
	"urlshortener/internal/shorturl"
)

// Handler is the HTTP adapter of the shorten slice (S5.3): it translates
// HTTP requests into use case calls and use case results/errors into HTTP
// responses. It contains no business logic — validation lives in the
// domain, orchestration in the use case.
type Handler struct {
	uc *UseCase
}

// NewHandler constructs the handler over its use case.
func NewHandler(uc *UseCase) *Handler {
	return &Handler{uc: uc}
}

// shortenRequest is the S2 request shape: {"url": "..."}.
type shortenRequest struct {
	URL string `json:"url"`
}

// Shorten handles POST /shorten (S2):
//
//   - 201 with {"code": "...", "short_url": "..."} on success;
//   - 422 RFC 9457 problem details for invalid input (domain validation
//     errors, S2/S8) — rejected input persists nothing;
//   - 400 problem details for a malformed JSON body (transport-level, so
//     distinct from a semantically invalid URL);
//   - 500 problem details for exhausted collision retries or
//     infrastructure failures — internal error text never reaches the
//     user (S8).
//
// S13: the handler renders NO error responses. Every failure — including
// malformed JSON, whose DETECTION is an HTTP concern that stays here — is
// attached via c.Error() as an httpx.ProblemError and the handler returns;
// the error middleware (internal/api) is the single place that renders the
// problem and logs the failure with the full cause chain. The success
// response below is the only body a handler writes.
func (h *Handler) Shorten(c *gin.Context) {
	var req shortenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		_ = c.Error(httpx.NewProblemError(httpx.Problem{
			Status: http.StatusBadRequest,
			Type:   httpx.TypeURI("malformed-json"),
			Detail: "the request body must be valid JSON with a \"url\" field",
		}, err))
		return
	}

	res, err := h.uc.Shorten(c.Request.Context(), req.URL)
	if err != nil {
		_ = c.Error(problemFor(err))
		return
	}

	c.JSON(http.StatusCreated, gin.H{
		"code":      res.Code,
		"short_url": res.ShortURL,
	})
}

// isInvalidInput reports whether err is a client-input error: the domain
// validation errors surfaced by the use case. Scheme rejection is a
// distinct domain value but still invalid input.
func isInvalidInput(err error) bool {
	return errors.Is(err, shorturl.ErrEmptyURL) ||
		errors.Is(err, shorturl.ErrInvalidURL) ||
		errors.Is(err, shorturl.ErrInvalidURLScheme)
}

// problemFor is THE single domain-error → HTTP mapping of the shorten
// slice (S9). It lives here, not in a shared map, by design (S5.4):
// the errors it inspects are shorten-slice/domain values; redirect and
// future slices own their own mappings, while httpx owns only the
// rendering. It returns an httpx.ProblemError (S13): the Problem is
// user-safe and gets rendered by the error middleware; the original
// error travels as the cause and reaches only the logs, never the user.
func problemFor(err error) *httpx.ProblemError {
	switch {
	case isInvalidInput(err):
		return httpx.NewProblemError(httpx.Problem{
			Status: http.StatusUnprocessableEntity,
			Type:   httpx.TypeURI("invalid-url"),
			Detail: err.Error(),
		}, err)
	case errors.Is(err, ErrCodeGenerationExhausted):
		return httpx.NewProblemError(httpx.Problem{
			Status: http.StatusInternalServerError,
			Type:   httpx.TypeURI("code-generation-exhausted"),
			Detail: "the short code could not be generated at this time",
		}, err)
	default:
		// Infrastructure failure: same generic treatment, no
		// internals leaked (S8).
		return httpx.NewProblemError(httpx.Problem{
			Status: http.StatusInternalServerError,
			Type:   httpx.TypeURI("internal-error"),
			Detail: "the request could not be processed at this time",
		}, err)
	}
}
