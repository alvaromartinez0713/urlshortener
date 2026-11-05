package redirect

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"urlshortener/internal/api/middleware"
	"urlshortener/internal/platform/httpx"
)

// getRedirect issues a GET /:code against a Gin engine wired like the
// composition root's stack (error middleware + handler) and returns the
// recorder. The response contract asserted below is identical before and
// after S13 (handlers attach, middleware renders) — behavior lock.
func getRedirect(t *testing.T, uc *UseCase, code string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ProblemRenderer(slog.New(slog.NewTextHandler(io.Discard, nil))))
	router.GET("/:code", NewHandler(uc).Redirect)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/"+code, nil))
	return rec
}

func TestRedirectHandlerMovedPermanentlyWithExactLocation(t *testing.T) {
	// S2: GET /:code -> 301 with Location set to the stored target,
	// byte-for-byte. Query string and fragment must survive untouched:
	// gin's c.Redirect delegates to net/http.Redirect, which hex-escapes
	// only non-ASCII bytes, so a pure-ASCII target must appear in the
	// Location header exactly as stored.
	su := mustRehydrate(t, "10", "https://example.com/a/b?q=1&lang=en#section-2")
	uc := NewUseCase(&fakeFinder{su: su})

	rec := getRedirect(t, uc, "10")

	if rec.Code != http.StatusMovedPermanently {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusMovedPermanently, rec.Body.String())
	}
	want := "https://example.com/a/b?q=1&lang=en#section-2"
	if got := rec.Header().Get("Location"); got != want {
		t.Errorf("Location = %q, want exactly %q (no escaping or rewriting surprises)", got, want)
	}
}

func TestRedirectHandlerUnknownCodeIsProblemDetails404(t *testing.T) {
	// S2 + S8: unknown code -> 404 as an RFC 9457 problem details
	// document, same shape as every other error answer in the API.
	uc := NewUseCase(&fakeFinder{err: ErrNotFound})

	rec := getRedirect(t, uc, "nope")

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusNotFound, rec.Body.String())
	}
	httpx.AssertProblem(t, rec, http.StatusNotFound, httpx.TypeURI("unknown-code"))
}

func TestRedirectHandlerInfraFailureIs500(t *testing.T) {
	// S8: no panic and no internal error text reaches the user; a port
	// infrastructure failure surfaces as a 500 problem document.
	uc := NewUseCase(&fakeFinder{err: errors.New("connection refused")})

	rec := getRedirect(t, uc, "10")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	httpx.AssertProblem(t, rec, http.StatusInternalServerError, httpx.TypeURI("internal-error"))
}
