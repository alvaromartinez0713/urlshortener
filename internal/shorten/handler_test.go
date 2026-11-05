package shorten

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"urlshortener/internal/api/middleware"
	"urlshortener/internal/platform/httpx"
)

// testLogger discards log output: these tests pin the HTTP contract, not
// the log stream (the middleware's records have their own tests).
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// postShorten issues a POST /shorten against a Gin engine wired like the
// composition root's stack (error middleware + handler) and returns the
// recorder. The response contract asserted below is identical before and
// after S13 (handlers attach, middleware renders) — behavior lock.
func postShorten(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	uc := NewUseCase(&fakeRepo{}, &fakeSequences{values: []uint64{62}})
	return postShortenWith(t, uc, body)
}

func postShortenWith(t *testing.T, uc *UseCase, body string) *httptest.ResponseRecorder {
	t.Helper()
	return postShortenBase(t, uc, "https://sho.rt", body)
}

// postShortenBase wires the composition-root stack with an explicit
// baseURL: short_url rendering (S15) is the handler's contract.
func postShortenBase(t *testing.T, uc *UseCase, baseURL, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ProblemRenderer(testLogger()))
	router.POST("/shorten", NewHandler(uc, baseURL).Shorten)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/shorten", strings.NewReader(body)))
	return rec
}

func TestShortenHandlerCreated(t *testing.T) {
	rec := postShorten(t, `{"url":"https://example.com/a/b?q=1"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var body struct {
		Code     string `json:"code"`
		ShortURL string `json:"short_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body.Code != "10" {
		t.Errorf(`body[code] = %q, want "10"`, body.Code)
	}
	if body.ShortURL != "https://sho.rt/10" {
		t.Errorf(`body[short_url] = %q, want "https://sho.rt/10"`, body.ShortURL)
	}
}

// TestShortenHandlerShortURLComposition (S15): the handler renders the
// public short URL from BaseURL; a trailing slash in BaseURL must not
// produce a double slash.
func TestShortenHandlerShortURLComposition(t *testing.T) {
	uc := NewUseCase(&fakeRepo{}, &fakeSequences{values: []uint64{62}})
	rec := postShortenBase(t, uc, "https://sho.rt/", `{"url":"https://example.com"}`)

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusCreated, rec.Body.String())
	}
	var body struct {
		ShortURL string `json:"short_url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body.ShortURL != "https://sho.rt/10" {
		t.Errorf(`body[short_url] = %q, want "https://sho.rt/10" (no double slash)`, body.ShortURL)
	}
}

func TestShortenHandlerInvalidURLIsProblemDetails422(t *testing.T) {
	// S2 + S8: invalid URL -> 422 as an RFC 9457 problem details document.
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid url", body: `{"url":"example.com"}`},
		{name: "empty url", body: `{"url":""}`},
		{name: "missing url field", body: `{}`},
		{name: "ftp scheme", body: `{"url":"ftp://example.com"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := postShorten(t, tc.body)

			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
			}
			httpx.AssertProblem(t, rec, http.StatusUnprocessableEntity, httpx.TypeURI("invalid-url"))
		})
	}
}

// TestShortenHandlerProblemDetailDoesNotEchoInput (S25): the problem
// detail is a FIXED user-safe message — the offending raw input travels
// only in the log cause chain, never back in the response body.
func TestShortenHandlerProblemDetailDoesNotEchoInput(t *testing.T) {
	rec := postShorten(t, `{"url":"ftp://attacker.example.com/payload"}`)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	httpx.AssertProblem(t, rec, http.StatusUnprocessableEntity, httpx.TypeURI("invalid-url"))

	var body struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if strings.Contains(body.Detail, "attacker.example.com") {
		t.Errorf("detail %q echoes the raw user input (S25 violation)", body.Detail)
	}
}

func TestShortenHandlerMalformedJSONIs400(t *testing.T) {
	rec := postShorten(t, `{"url": not json`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	httpx.AssertProblem(t, rec, http.StatusBadRequest, httpx.TypeURI("malformed-json"))
}

func TestShortenHandlerExhaustedRetriesIs500(t *testing.T) {
	// S8: no panic and no internal error text reaches the user; a server
	// failure surfaces as a 500 problem document.
	uc := NewUseCase(
		&fakeRepo{saveErrs: []error{ErrCodeCollision, ErrCodeCollision, ErrCodeCollision}},
		&fakeSequences{values: []uint64{1, 2, 3}},
	)
	rec := postShortenWith(t, uc, `{"url":"https://example.com"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	httpx.AssertProblem(t, rec, http.StatusInternalServerError, httpx.TypeURI("code-generation-exhausted"))
}
