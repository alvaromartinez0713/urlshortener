package shorten

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"urlshortener/internal/platform/httpx"
)

// Handler is the HTTP adapter of the shorten slice (S5.3): it translates
// HTTP requests into use case calls and use case results/errors into HTTP
// responses. It contains no business logic — validation lives in the
// domain, orchestration in the use case.
type Handler struct {
	uc      *UseCase
	baseURL string
}

// NewHandler constructs the handler over its use case. baseURL is the
// public origin of generated short URLs (e.g. "https://sho.rt"): a
// PRESENTATION setting (S15) — it shapes the response, not the business
// outcome — so it lives here, in the HTTP adapter, not in the use case.
// Trailing slashes are trimmed so "https://sho.rt/" and "https://sho.rt"
// render identical short URLs.
func NewHandler(uc *UseCase, baseURL string) *Handler {
	return &Handler{uc: uc, baseURL: strings.TrimRight(baseURL, "/")}
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

	su, err := h.uc.Shorten(c.Request.Context(), req.URL)
	if err != nil {
		_ = c.Error(problemFor(err))
		return
	}

	// S15: the RESPONSE is the handler's business — the entity from the
	// use case is rendered here, public short URL included.
	c.JSON(http.StatusCreated, gin.H{
		"code":      su.Code(),
		"short_url": h.baseURL + "/" + su.Code(),
	})
}
