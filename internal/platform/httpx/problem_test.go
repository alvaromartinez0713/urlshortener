package httpx

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
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
	if body["title"] != "Unprocessable Entity" {
		t.Errorf("body[title] = %v, want %q (derived from the status)", body["title"], "Unprocessable Entity")
	}
	if body["status"] != float64(http.StatusUnprocessableEntity) {
		t.Errorf("body[status] = %v, want %d", body["status"], http.StatusUnprocessableEntity)
	}
	if body["detail"] != "not an absolute http/https URL" {
		t.Errorf("body[detail] = %v, want the given detail verbatim", body["detail"])
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
