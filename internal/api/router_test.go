package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"urlshortener/internal/platform/httpx"
)

func TestHealthzReturnsOK(t *testing.T) {
	router := NewRouter()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}

	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body["status"] != "ok" {
		t.Fatalf(`body["status"] = %q, want "ok" (body: %q)`, body["status"], rec.Body.String())
	}
}

func TestUnknownRouteReturnsProblemDetailsJSON(t *testing.T) {
	router := NewRouter()

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	httpx.AssertProblem(t, rec, http.StatusNotFound, httpx.TypeURI("route-not-found"))
}
