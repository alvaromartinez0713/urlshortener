package shorten

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

// postShorten issues a POST /shorten against a Gin engine wired with the
// handler under test and returns the recorder.
func postShorten(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	uc := NewUseCase(&fakeRepo{}, &fakeSequences{values: []uint64{62}}, "https://sho.rt")
	return postShortenWith(t, uc, body)
}

func postShortenWith(t *testing.T, uc *UseCase, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.POST("/shorten", NewHandler(uc).Shorten)

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
			assertProblemDetails(t, rec, http.StatusUnprocessableEntity)
		})
	}
}

func TestShortenHandlerMalformedJSONIs400(t *testing.T) {
	rec := postShorten(t, `{"url": not json`)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
	assertProblemDetails(t, rec, http.StatusBadRequest)
}

func TestShortenHandlerExhaustedRetriesIs500(t *testing.T) {
	// S8: no panic and no internal error text reaches the user; a server
	// failure surfaces as a 500 problem document.
	uc := NewUseCase(
		&fakeRepo{saveErrs: []error{ErrCodeCollision, ErrCodeCollision, ErrCodeCollision}},
		&fakeSequences{values: []uint64{1, 2, 3}},
		"https://sho.rt",
	)
	rec := postShortenWith(t, uc, `{"url":"https://example.com"}`)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	assertProblemDetails(t, rec, http.StatusInternalServerError)
}

// assertProblemDetails verifies the RFC 9457 shape used across the API
// (same shape as internal/api NoRoute): type, title, status, detail.
func assertProblemDetails(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int) {
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
