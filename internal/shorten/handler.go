package shorten

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

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
func (h *Handler) Shorten(c *gin.Context) {
	var req shortenRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		writeProblem(c, http.StatusBadRequest, "Bad Request",
			"the request body must be valid JSON with a \"url\" field")
		return
	}

	res, err := h.uc.Shorten(c.Request.Context(), req.URL)
	if err != nil {
		switch {
		case isInvalidInput(err):
			writeProblem(c, http.StatusUnprocessableEntity, "Unprocessable Entity", err.Error())
		case errors.Is(err, ErrCodeGenerationExhausted):
			writeProblem(c, http.StatusInternalServerError, "Internal Server Error",
				"the short code could not be generated at this time")
		default:
			// Infrastructure failure: same generic treatment, no
			// internals leaked (S8).
			writeProblem(c, http.StatusInternalServerError, "Internal Server Error",
				"the request could not be processed at this time")
		}
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

// writeProblem answers with an RFC 9457 problem details document (S8),
// the same shape as internal/api's NoRoute handler.
func writeProblem(c *gin.Context, status int, title, detail string) {
	c.AbortWithStatusJSON(status, gin.H{
		"type":   "about:blank",
		"title":  title,
		"status": status,
		"detail": detail,
	})
}
