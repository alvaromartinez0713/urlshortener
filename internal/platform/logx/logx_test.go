package logx_test

// Unit tests for the context-aware slog wrapper (S30/T32): when the
// record's context carries a request id, every record gains a
// request_id attribute BEFORE delegating to the wrapped handler; without
// one, records pass through untouched. Pure stdlib.

import (
	"context"
	"log/slog"
	"testing"
	"time"

	"urlshortener/internal/platform/logx"
	"urlshortener/internal/platform/requestid"
)

type record struct {
	level slog.Level
	msg   string
	attrs map[string]any
}

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

func TestWrapAddsRequestIDFromContext(t *testing.T) {
	h := &captureHandler{}
	logger := slog.New(logx.Wrap(h))

	ctx := requestid.Put(context.Background(), "corr-42")
	logger.InfoContext(ctx, "handling request", slog.String("method", "GET"))

	if len(h.records) != 1 {
		t.Fatalf("records = %d, want 1", len(h.records))
	}
	got := h.records[0]
	if got.msg != "handling request" {
		t.Errorf("message = %q, want the original", got.msg)
	}
	if id, _ := got.attrs["request_id"].(string); id != "corr-42" {
		t.Errorf("attrs[request_id] = %v, want %q", got.attrs["request_id"], "corr-42")
	}
	// The wrapper only ADDS the correlation attr; the record's own attrs
	// and level must reach the wrapped handler untouched.
	if got.attrs["method"] != "GET" {
		t.Errorf("attrs[method] = %v, want the original attr preserved", got.attrs["method"])
	}
	if got.level != slog.LevelInfo {
		t.Errorf("level = %v, want the original level", got.level)
	}
}

func TestWrapWithoutRequestIDLeavesRecordUntouched(t *testing.T) {
	h := &captureHandler{}
	logger := slog.New(logx.Wrap(h))

	logger.WarnContext(context.Background(), "no correlation here", slog.String("k", "v"))

	if len(h.records) != 1 {
		t.Fatalf("records = %d, want 1", len(h.records))
	}
	got := h.records[0]
	if _, present := got.attrs["request_id"]; present {
		t.Errorf("attrs[request_id] = %v, want no request_id without one in ctx", got.attrs["request_id"])
	}
	if got.attrs["k"] != "v" || got.level != slog.LevelWarn || got.msg != "no correlation here" {
		t.Errorf("record = %+v, want the original record untouched", got)
	}
}

func TestWrapDelegatesEnabled(t *testing.T) {
	h := &captureHandler{}
	if !logx.Wrap(h).Enabled(context.Background(), slog.LevelDebug) {
		t.Error("Wrap did not delegate Enabled to the wrapped handler")
	}
	_ = time.Now // keep the time import anchored if assertions evolve
}

// TestWrapInjectsClientRequestID (S31/T35): the client-supplied
// correlation id travels under its OWN attribute, never merged into
// request_id — the server-generated id stays authoritative.
func TestWrapInjectsClientRequestID(t *testing.T) {
	h := &captureHandler{}
	ctx := requestid.PutClient(context.Background(), "client-correlation.42")
	if err := logx.Wrap(h).Handle(ctx, slog.NewRecord(time.Now(), slog.LevelWarn, "with client id", 0)); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if len(h.records) != 1 {
		t.Fatalf("records = %d, want 1", len(h.records))
	}
	if got := h.records[0].attrs["client_request_id"]; got != "client-correlation.42" {
		t.Errorf("attrs[client_request_id] = %v, want the client value under its own attribute", got)
	}
	if _, present := h.records[0].attrs["request_id"]; present {
		t.Errorf("attrs[request_id] = %v, want NO request_id when only a client id is in ctx", h.records[0].attrs["request_id"])
	}
}
