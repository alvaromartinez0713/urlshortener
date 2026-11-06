package httpx

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"urlshortener/internal/platform/requestid"
)

// Contract tests for the shared problem-details builder (S9): the wire
// shape every handler and the NoRoute fallback must emit. These
// assertions are deliberately written out long-handled (not via
// AssertProblem) so the builder is checked independently of its own test
// helper.
func TestWriteProblemRendersCompleteRFC9457(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	WriteProblem(c, Problem{
		Status: http.StatusUnprocessableEntity,
		Type:   TypeURI("invalid-url"),
		Detail: "not an absolute http/https URL",
	})

	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnprocessableEntity)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", got)
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	wantType := ProblemTypeBase + "invalid-url"
	if body["type"] != wantType {
		t.Errorf("body[type] = %v, want %q", body["type"], wantType)
	}
	if body["title"] != "Unprocessable Content" {
		t.Errorf("body[title] = %v, want %q (RFC 9457 registered title for 422)", body["title"], "Unprocessable Content")
	}
	if body["status"] != float64(http.StatusUnprocessableEntity) {
		t.Errorf("body[status] = %v, want %d", body["status"], http.StatusUnprocessableEntity)
	}
	if body["detail"] != "not an absolute http/https URL" {
		t.Errorf("body[detail] = %v, want the given detail verbatim", body["detail"])
	}
	// RFC 9457 completeness (S21): the renderer fills instance per
	// request — a correlation identifier for THIS occurrence.
	inst, ok := body["instance"].(string)
	if !ok || inst == "" {
		t.Errorf("body[instance] missing or empty: %v", body["instance"])
	}
	if !strings.HasPrefix(inst, "/") {
		t.Errorf("body[instance] = %q, want the request path prefix", inst)
	}
}

func TestWriteProblemInstanceReusesRequestIDFromContext(t *testing.T) {
	// S30/T33: when the request context carries a request id (put there
	// by the RequestID middleware), the generated instance member reuses
	// it — the id the user quotes and the id in the logs are the same.
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodGet, "/shorten", nil).
		WithContext(requestid.Put(context.Background(), "corr-9"))

	WriteProblem(c, Problem{
		Status: http.StatusUnprocessableEntity,
		Type:   TypeURI("invalid-url"),
		Detail: "not an absolute http/https URL",
	})

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body["instance"] != "/shorten#corr-9" {
		t.Errorf("body[instance] = %v, want the ctx request id reused (\"/shorten#corr-9\")", body["instance"])
	}
}

func TestWriteProblemRendersExtensionsAndExplicitInstance(t *testing.T) {
	// S21: extension members render as top-level members; an explicit
	// instance is rendered verbatim (RFC 9457 allows absolute URLs and
	// relative references).
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/shorten", nil)

	WriteProblem(c, Problem{
		Status:     http.StatusBadRequest,
		Type:       TypeURI("malformed-json"),
		Detail:     "d",
		Instance:   "/shorten#req-42",
		Extensions: map[string]any{"errors": map[string]string{"url": "required"}},
	})

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not JSON: %v (%q)", err, rec.Body.String())
	}
	if body["instance"] != "/shorten#req-42" {
		t.Errorf("body[instance] = %v, want the explicit value verbatim", body["instance"])
	}
	errs, ok := body["errors"].(map[string]any)
	if !ok || errs["url"] != "required" {
		t.Errorf("body[errors] = %v, want the extension member rendered", body["errors"])
	}
}

func TestTypeURIJoinsDocumentedBase(t *testing.T) {
	// S9: type URIs are meaningful URIs under the one documented base,
	// never "about:blank".
	if !strings.HasPrefix(TypeURI("invalid-url"), "https://") {
		t.Errorf("TypeURI = %q, want an absolute https URI", TypeURI("invalid-url"))
	}
	if TypeURI("invalid-url") != ProblemTypeBase+"invalid-url" {
		t.Errorf("TypeURI = %q, want base + slug", TypeURI("invalid-url"))
	}
}

func TestProblemTitleDerivesFromStatus(t *testing.T) {
	p := Problem{Status: http.StatusNotFound, Type: TypeURI("unknown-code")}
	if p.Title() != "Not Found" {
		t.Errorf("Title = %q, want %q", p.Title(), "Not Found")
	}
	// RFC 9457 recommendation: 422's title is "Unprocessable Content"
	// (RFC 9110 renamed the reason phrase).
	p422 := Problem{Status: http.StatusUnprocessableEntity}
	if p422.Title() != "Unprocessable Content" {
		t.Errorf("Title = %q, want %q", p422.Title(), "Unprocessable Content")
	}
}

func TestWriteProblemAbortsTheHandlerChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/x",
		func(c *gin.Context) {
			WriteProblem(c, Problem{Status: http.StatusBadRequest, Type: TypeURI("malformed-json"), Detail: "d"})
		},
		func(c *gin.Context) {
			t.Errorf("later handler ran after WriteProblem: chain was not aborted")
		},
	)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	AssertProblem(t, rec, http.StatusBadRequest, TypeURI("malformed-json"))
}
