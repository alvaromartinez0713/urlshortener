package api

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"urlshortener/internal/platform/httpx"
)

// testLogger discards log output: these tests pin the HTTP contract, not
// the log stream (the middleware's records have their own tests in
// internal/api/middleware).
func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHealthzReturnsOK(t *testing.T) {
	router := NewRouter(testLogger())

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
	router := NewRouter(testLogger())

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/nope", nil))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	httpx.AssertProblem(t, rec, http.StatusNotFound, httpx.TypeURI("route-not-found"))
}

// TestAccessLogRecordsFinalStatusForErrorResponses pins the middleware
// ORDER (S13): the request logger sits OUTSIDE the error middleware, so
// its access line is written after the error response is rendered and
// must record the FINAL status. A 422 logged as 200 would make the
// access log useless for the team.
func TestAccessLogRecordsFinalStatusForErrorResponses(t *testing.T) {
	var buf strings.Builder
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	router := NewRouter(logger)

	// Attach a client-class error the way handlers now do (S13).
	router.POST("/boom", func(c *gin.Context) {
		_ = c.Error(httpx.NewProblemError(httpx.Problem{
			Status: http.StatusUnprocessableEntity,
			Type:   httpx.TypeURI("invalid-url"),
			Detail: "the url is not valid",
		}, nil))
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/boom", nil))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}

	var accessLine string
	for line := range strings.SplitSeq(strings.TrimSpace(buf.String()), "\n") {
		var rec2 map[string]any
		if err := json.Unmarshal([]byte(line), &rec2); err != nil {
			t.Fatalf("log line is not JSON: %v (%q)", err, line)
		}
		if rec2["msg"] == "request" && rec2["path"] == "/boom" {
			accessLine = line
		}
	}
	if accessLine == "" {
		t.Fatalf("no access-log record for /boom in:\n%s", buf.String())
	}
	var rec2 map[string]any
	_ = json.Unmarshal([]byte(accessLine), &rec2)
	if rec2["status"] != float64(http.StatusUnprocessableEntity) {
		t.Errorf("access log status = %v, want %d (final status, not 200)", rec2["status"], http.StatusUnprocessableEntity)
	}
}
