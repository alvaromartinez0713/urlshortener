package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// AssertProblem is an EXPORTED TEST HELPER asserting the full RFC 9457
// wire contract of a problem response: Content-Type
// application/problem+json and the type (exactly wantType), title,
// status and detail members. Every slice's handler tests and the router
// test assert problem responses through this one helper instead of
// hand-copied assertion copies (S9, S12).
//
// It lives in the production package on purpose: httpx owns the
// problem-details contract, so its assertion travels with it. The
// testing/httptest imports are acceptable for this adapter-level
// package; no domain or framework code depends on them.
func AssertProblem(t testing.TB, rec *httptest.ResponseRecorder, wantStatus int, wantType string) {
	t.Helper()
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body["type"] != wantType {
		t.Errorf("body[type] = %v, want %q", body["type"], wantType)
	}
	if wantTitle := http.StatusText(wantStatus); body["title"] != wantTitle {
		t.Errorf("body[title] = %v, want %q", body["title"], wantTitle)
	}
	if body["status"] != float64(wantStatus) {
		t.Errorf("body[status] = %v, want %d", body["status"], wantStatus)
	}
	if detail, ok := body["detail"].(string); !ok || detail == "" {
		t.Errorf("body[detail] = %v, want a non-empty RFC 9457 detail", body["detail"])
	}
}
