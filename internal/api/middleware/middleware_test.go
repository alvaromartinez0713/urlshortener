package middleware_test

// S13: the error middleware is the SINGLE place that renders error
// responses and logs them. These tests pin its classification contract:
// ProblemError 4xx -> WARN + mapped problem; ProblemError 5xx -> ERROR +
// mapped problem (chain only in the log, never in the body); plain error
// -> ERROR + generic 500 problem with no internals; no error -> nothing.
// The request logger emits one INFO access line per request.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"urlshortener/internal/api/middleware"
	"urlshortener/internal/platform/httpx"
	"urlshortener/internal/platform/logx"
)

// record is one captured slog record with its attributes materialized.
type record struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

// captureHandler is a minimal in-memory slog.Handler so tests can assert
// on the exact records (level, message, attrs) the middleware emits.
type captureHandler struct{ records []record }

func (h *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *captureHandler) Handle(_ context.Context, r slog.Record) error {
	rec := record{level: r.Level, msg: r.Message, attrs: map[string]any{}}
	r.Attrs(func(a slog.Attr) bool { rec.attrs[a.Key] = a.Value.Any(); return true })
	h.records = append(h.records, rec)
	return nil
}

func (h *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *captureHandler) WithGroup(string) slog.Handler      { return h }

func newCaptureLogger() (*slog.Logger, *captureHandler) {
	h := &captureHandler{}
	return slog.New(h), h
}

func newRouter(logger *slog.Logger, handle gin.HandlerFunc) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ProblemRenderer(logger))
	router.GET("/thing", handle)
	return router
}

func recordsAt(capture *captureHandler, level slog.Level) []record {
	var out []record
	for _, r := range capture.records {
		if r.level == level {
			out = append(out, r)
		}
	}
	return out
}

func TestProblemRendererClientProblemRendersMappedProblemAndLogsWarn(t *testing.T) {
	// 4xx: well-mapped, user-safe problem rendered; WARN for the team.
	logger, capture := newCaptureLogger()
	cause := errors.New("domain: url must be an absolute http/https URL")
	router := newRouter(logger, func(c *gin.Context) {
		_ = c.Error(httpx.NewProblemError(httpx.Problem{
			Status: http.StatusUnprocessableEntity,
			Type:   httpx.TypeURI("invalid-url"),
			Detail: "url must be an absolute http/https URL",
		}, cause))
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thing", nil))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	httpx.AssertProblem(t, rec, http.StatusUnprocessableEntity, httpx.TypeURI("invalid-url"))

	warns := recordsAt(capture, slog.LevelWarn)
	if len(warns) != 1 {
		t.Fatalf("WARN records = %d, want exactly 1 (all: %+v)", len(warns), capture.records)
	}
	assertRequestContextAttrs(t, warns[0], http.StatusUnprocessableEntity)
	if got := warns[0].attrs["error"]; got != "url must be an absolute http/https URL" {
		t.Errorf(`attrs[error] = %v, want the mapped detail`, got)
	}
	if chain, _ := warns[0].attrs["chain"].(string); !strings.Contains(chain, "domain: url must be an absolute http/https URL") {
		t.Errorf("attrs[chain] = %q, want the full cause chain", chain)
	}
	if errs := recordsAt(capture, slog.LevelError); len(errs) != 0 {
		t.Errorf("client-class error logged at ERROR: %+v", errs)
	}
}

func TestProblemRendererInfraProblemLogsErrorWithChainButNeverInBody(t *testing.T) {
	// 5xx ProblemError: the mapped detail is already user-safe (the slice
	// guarantees it), so it renders; the internal cause goes to the ERROR
	// log only.
	logger, capture := newCaptureLogger()
	cause := errors.New("connection refused")
	router := newRouter(logger, func(c *gin.Context) {
		_ = c.Error(httpx.NewProblemError(httpx.Problem{
			Status: http.StatusInternalServerError,
			Type:   httpx.TypeURI("internal-error"),
			Detail: "the request could not be processed at this time",
		}, cause))
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thing", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	httpx.AssertProblem(t, rec, http.StatusInternalServerError, httpx.TypeURI("internal-error"))
	if strings.Contains(rec.Body.String(), "connection refused") {
		t.Errorf("response body leaked the internal cause: %s", rec.Body.String())
	}

	errs := recordsAt(capture, slog.LevelError)
	if len(errs) != 1 {
		t.Fatalf("ERROR records = %d, want exactly 1 (all: %+v)", len(errs), capture.records)
	}
	assertRequestContextAttrs(t, errs[0], http.StatusInternalServerError)
	if chain, _ := errs[0].attrs["chain"].(string); !strings.Contains(chain, "connection refused") {
		t.Errorf("attrs[chain] = %q, want the full cause chain", chain)
	}
}

func TestProblemRendererPlainErrorRendersGeneric500WithoutInternals(t *testing.T) {
	// A plain (non-Problem) error has unknown provenance: the user gets
	// the generic 500 problem; the team gets the full chain + context.
	logger, capture := newCaptureLogger()
	secret := "postgres://app:hunter2@db.internal:5432/urls"
	router := newRouter(logger, func(c *gin.Context) {
		_ = c.Error(errors.New("pq: dial tcp " + secret))
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thing", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusInternalServerError, rec.Body.String())
	}
	httpx.AssertProblem(t, rec, http.StatusInternalServerError, httpx.TypeURI("internal-error"))
	if strings.Contains(rec.Body.String(), "hunter2") {
		t.Errorf("response body leaked internal error text: %s", rec.Body.String())
	}

	errs := recordsAt(capture, slog.LevelError)
	if len(errs) != 1 {
		t.Fatalf("ERROR records = %d, want exactly 1 (all: %+v)", len(errs), capture.records)
	}
	assertRequestContextAttrs(t, errs[0], http.StatusInternalServerError)
	if chain, _ := errs[0].attrs["chain"].(string); !strings.Contains(chain, secret) {
		t.Errorf("attrs[chain] = %q, want the full internal chain", chain)
	}
}

func TestProblemRendererNoErrorNoErrorLogAndRequestLoggerLogsAccess(t *testing.T) {
	// No c.Error -> the error middleware logs nothing; the request
	// logger still emits its one INFO access line with the request
	// context (S13: access logs are the request logger's job).
	logger, capture := newCaptureLogger()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ProblemRenderer(logger), middleware.RequestLogger(logger))
	router.GET("/healthz", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"status": "ok"}) })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := recordsAt(capture, slog.LevelWarn); len(got) != 0 {
		t.Errorf("WARN records = %d, want 0: %+v", len(got), got)
	}
	if got := recordsAt(capture, slog.LevelError); len(got) != 0 {
		t.Errorf("ERROR records = %d, want 0: %+v", len(got), got)
	}
	infos := recordsAt(capture, slog.LevelInfo)
	if len(infos) != 1 {
		t.Fatalf("INFO records = %d, want exactly 1 access line: %+v", len(infos), capture.records)
	}
	acc := infos[0]
	if acc.msg != "request" {
		t.Errorf("access log message = %q, want %q", acc.msg, "request")
	}
	if acc.attrs["method"] != "GET" || acc.attrs["path"] != "/healthz" {
		t.Errorf("access log method/path = %v/%v, want GET//healthz", acc.attrs["method"], acc.attrs["path"])
	}
	if acc.attrs["status"] != int64(http.StatusOK) {
		t.Errorf("access log status = %v, want %d", acc.attrs["status"], http.StatusOK)
	}
	if _, ok := acc.attrs["latency"]; !ok {
		t.Error("access log has no latency attr")
	}
	if _, ok := acc.attrs["client_ip"]; !ok {
		t.Error("access log has no client_ip attr")
	}
}

func TestProblemRendererDoesNotDoubleRenderAfterResponseWritten(t *testing.T) {
	// If a response was already written (e.g. a handler attached an error
	// after c.Redirect), the middleware logs but must NOT append a second
	// body to the response.
	logger, capture := newCaptureLogger()
	router := newRouter(logger, func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
		_ = c.Error(httpx.NewProblemError(httpx.Problem{
			Status: http.StatusInternalServerError,
			Type:   httpx.TypeURI("internal-error"),
			Detail: "the request could not be processed at this time",
		}, errors.New("late failure")))
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thing", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want the already-written 200", rec.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("body is not a single JSON document (%v): %q — double render?", err, rec.Body.String())
	}
	if len(body) != 1 || body["ok"] != true {
		t.Errorf("body = %s, want the untouched {\"ok\":true} response", rec.Body.String())
	}
	if errs := recordsAt(capture, slog.LevelError); len(errs) != 1 {
		t.Errorf("ERROR records = %d, want 1 (late errors are still logged)", len(errs))
	}
}

// assertRequestContextAttrs checks the method/path/status context fields
// the error log must carry for the team (S13).
func assertRequestContextAttrs(t *testing.T, r record, wantStatus int) {
	t.Helper()
	if r.attrs["method"] != "GET" {
		t.Errorf("attrs[method] = %v, want GET", r.attrs["method"])
	}
	if r.attrs["path"] != "/thing" {
		t.Errorf("attrs[path] = %v, want /thing", r.attrs["path"])
	}
	if r.attrs["status"] != int64(wantStatus) {
		t.Errorf("attrs[status] = %v, want %d", r.attrs["status"], wantStatus)
	}
}

// TestProblemRendererLogsEveryAttachedError (S18): a handler may attach
// more than one error; the renderer must log ALL of them, not only the
// last one.
func TestProblemRendererLogsEveryAttachedError(t *testing.T) {
	logger, capture := newCaptureLogger()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ProblemRenderer(logger))
	router.GET("/thing", func(c *gin.Context) {
		_ = c.Error(httpx.NewProblemError(httpx.Problem{
			Status: http.StatusNotFound,
			Type:   httpx.TypeURI("unknown-code"),
			Detail: "first failure",
		}, nil))
		_ = c.Error(errors.New("second failure"))
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thing", nil))

	failures := 0
	for _, r := range capture.records {
		if r.msg == "request error" {
			failures++
		}
	}
	if failures != 2 {
		t.Errorf("request error records = %d, want 2 (every attached error is logged; all: %+v)", failures, capture.records)
	}
}

// TestProblemRendererLogsJoinedErrorChain (S18): the chain printer must
// traverse errors.Join trees (Unwrap() []error), not only single-unwrap
// chains — a joined cause truncates every other unwrapping printer.
func TestProblemRendererLogsJoinedErrorChain(t *testing.T) {
	logger, capture := newCaptureLogger()
	cause := errors.Join(errors.New("disk full"), errors.New("quota exceeded"))
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.ProblemRenderer(logger))
	router.GET("/thing", func(c *gin.Context) {
		_ = c.Error(httpx.NewProblemError(httpx.Problem{
			Status: http.StatusInternalServerError,
			Type:   httpx.TypeURI("internal-error"),
			Detail: "the request could not be processed at this time",
		}, cause))
	})

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thing", nil))

	var chains []string
	for _, r := range capture.records {
		if r.msg == "request error" {
			chains = append(chains, r.attrs["chain"].(string))
		}
	}
	if len(chains) != 1 {
		t.Fatalf("request error records = %d, want 1 (all: %+v)", len(chains), capture.records)
	}
	if !strings.Contains(chains[0], "disk full") || !strings.Contains(chains[0], "quota exceeded") {
		t.Errorf("chain = %q, want both joined causes present", chains[0])
	}
}

// --- RequestID middleware (S30/T33) -------------------------------------
//
// The request id middleware runs FIRST in the chain: it puts a
// correlation id into the request context and echoes it as the
// X-Request-ID response header. The logx wrapper turns that context id
// into a request_id attribute on EVERY record of the request, so the
// access line and the error line of one request grep together — and the
// id the user quotes from a problem document is the id in the logs.

var responseIDShape = regexp.MustCompile(`^[0-9a-f]{16}$`)

// newCorrelatedRouter wires the production middleware ORDER (Recovery >
// RequestID > RequestLogger > ProblemRenderer) around a handler that
// fails with a client-class problem, so one request exercises both the
// INFO access record and the WARN failure record.
func newCorrelatedRouter(logger *slog.Logger) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(gin.Recovery(), middleware.RequestID(), middleware.RequestLogger(logger), middleware.ProblemRenderer(logger))
	router.GET("/thing", func(c *gin.Context) {
		_ = c.Error(httpx.NewProblemError(httpx.Problem{
			Status: http.StatusUnprocessableEntity,
			Type:   httpx.TypeURI("invalid-url"),
			Detail: "url must be an absolute http/https URL",
		}, errors.New("domain: url must be an absolute http/https URL")))
	})
	return router
}

func TestRequestIDCorrelatesHeaderAndBothLogRecords(t *testing.T) {
	// One request: the response header, the INFO access record and the
	// WARN failure record must all carry the SAME request id.
	capture := &captureHandler{}
	logger := slog.New(logx.Wrap(capture))
	router := newCorrelatedRouter(logger)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/thing", nil))

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusUnprocessableEntity, rec.Body.String())
	}
	header := rec.Header().Get("X-Request-ID")
	if !responseIDShape.MatchString(header) {
		t.Fatalf("X-Request-ID header = %q, want a fresh 16-hex-char id", header)
	}

	infos := recordsAt(capture, slog.LevelInfo)
	if len(infos) != 1 {
		t.Fatalf("INFO records = %d, want 1 access line (all: %+v)", len(infos), capture.records)
	}
	if id, _ := infos[0].attrs["request_id"].(string); id != header {
		t.Errorf("access record request_id = %q, want the response header %q", id, header)
	}
	warns := recordsAt(capture, slog.LevelWarn)
	if len(warns) != 1 {
		t.Fatalf("WARN records = %d, want 1 failure record (all: %+v)", len(warns), capture.records)
	}
	if id, _ := warns[0].attrs["request_id"].(string); id != header {
		t.Errorf("failure record request_id = %q, want the response header %q", id, header)
	}
}

// TestRequestIDClientHeaderNeverChoosesInternalID (S31/T35): the client
// NEVER chooses the request id. A VALID incoming X-Request-ID is preserved
// separately as client_request_id (logs) — the response header and every
// log record carry the SERVER-GENERATED internal id.
func TestRequestIDClientHeaderNeverChoosesInternalID(t *testing.T) {
	capture := &captureHandler{}
	logger := slog.New(logx.Wrap(capture))
	router := newCorrelatedRouter(logger)

	req := httptest.NewRequest(http.MethodGet, "/thing", nil)
	req.Header.Set("X-Request-ID", "client-correlation.42")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	header := rec.Header().Get("X-Request-ID")
	if header == "client-correlation.42" {
		t.Fatalf("X-Request-ID = %q, want a server-generated id (the client must not choose it)", header)
	}
	if !responseIDShape.MatchString(header) {
		t.Fatalf("X-Request-ID = %q, want a fresh 16-hex-char id", header)
	}
	for _, want := range []struct {
		level slog.Level
		what  string
	}{
		{slog.LevelInfo, "access"},
		{slog.LevelWarn, "failure"},
	} {
		recs := recordsAt(capture, want.level)
		if len(recs) != 1 {
			t.Fatalf("%s records = %d, want 1 (all: %+v)", want.what, len(recs), capture.records)
		}
		if got := recs[0].attrs["request_id"]; got == "client-correlation.42" {
			t.Errorf("%s record request_id = %v, want the server-generated internal id", want.what, got)
		}
		if got := recs[0].attrs["client_request_id"]; got != "client-correlation.42" {
			t.Errorf("%s record client_request_id = %v, want the sanitized incoming value", want.what, got)
		}
	}
}

func TestRequestIDRejectsInvalidIncomingHeader(t *testing.T) {
	// An incoming id with characters outside the allowed set must never
	// be echoed back anywhere (S31): a fresh internal id is generated AND
	// no client_request_id exists at all.
	capture := &captureHandler{}
	logger := slog.New(logx.Wrap(capture))
	router := newCorrelatedRouter(logger)

	const hostile = "bad id\nX-Evil: 1"
	req := httptest.NewRequest(http.MethodGet, "/thing", nil)
	req.Header.Set("X-Request-ID", hostile)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	header := rec.Header().Get("X-Request-ID")
	if header == hostile {
		t.Fatal("X-Request-ID echoed the invalid incoming value verbatim")
	}
	if !responseIDShape.MatchString(header) {
		t.Errorf("X-Request-ID = %q, want a fresh valid id replacing the hostile one", header)
	}
	if strings.Contains(header, "X-Evil") || strings.ContainsAny(header, " \n\r") {
		t.Errorf("X-Request-ID = %q, leaked attacker-controlled bytes", header)
	}
	// S31: a REJECTED incoming value is not preserved at all — no
	// client_request_id may exist for hostile input.
	for _, r := range append(recordsAt(capture, slog.LevelInfo), recordsAt(capture, slog.LevelWarn)...) {
		if _, present := r.attrs["client_request_id"]; present {
			t.Errorf("record %+v carries client_request_id for a rejected incoming value", r)
		}
	}
}
