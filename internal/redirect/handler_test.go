package redirect

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
)

// getRedirect issues a GET /:code against a Gin engine wired with the
// handler under test and returns the recorder.
func getRedirect(t *testing.T, uc *UseCase, code string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
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
	finder := &fakeFinder{target: "https://example.com/a/b?q=1&lang=en#section-2"}
	uc := NewUseCase(finder)

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
	assertRedirectProblemDetails(t, rec, http.StatusNotFound)
}

func TestRedirectHandlerInfraFailureIs500(t *testing.T) {
	// S8: no panic and no internal error text reaches the user; a port
	// infrastructure failure surfaces as a 500 problem document.
	uc := NewUseCase(&fakeFinder{err: errors.New("connection refused")})

	rec := getRedirect(t, uc, "10")

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	assertRedirectProblemDetails(t, rec, http.StatusInternalServerError)
}

// assertRedirectProblemDetails verifies the RFC 9457 shape used across
// the API (same shape as internal/api NoRoute and the shorten slice):
// type, title, status, detail.
func assertRedirectProblemDetails(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body["type"] == "" {
		t.Error("body[type] is empty, want RFC 9457 type")
	}
	if body["title"] == "" {
		t.Error("body[title] is empty, want RFC 9457 title")
	}
	if body["status"] != float64(wantStatus) {
		t.Errorf("body[status] = %v, want %d", body["status"], wantStatus)
	}
	if body["detail"] == "" {
		t.Error("body[detail] is empty, want RFC 9457 detail")
	}
}
