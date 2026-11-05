// Package httpx is the shared adapter-level toolkit for pure HTTP
// transport concerns. It owns the ONE RFC 9457 problem-details
// implementation used by every handler and the NoRoute fallback (S9),
// so no slice hand-copies a JSON error shape.
//
// It deliberately imports no domain package: rendering a problem is a
// dumb adapter job. Mapping a domain error to a problem (status, type
// slug, detail) stays in the slice that owns the error (S5.4) — each
// slice builds an httpx.Problem and hands it to WriteProblem.
package httpx

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"

	"github.com/gin-gonic/gin"
)

// ProblemTypeBase is the documented base URI for every problem "type"
// this API emits (RFC 9457). Each problem appends one stable slug that
// identifies it, e.g. TypeURI("invalid-url") →
// https://alvaromartinez0713.github.io/urlshortener/problems/invalid-url.
// The base points at the project's site, where each slug can gain a
// human-readable explanation page without changing the API.
const ProblemTypeBase = "https://alvaromartinez0713.github.io/urlshortener/problems/"

// TypeURI returns the full RFC 9457 type URI for a problem slug.
func TypeURI(slug string) string {
	return ProblemTypeBase + slug
}

// Problem is one RFC 9457 problem document ready to render. Slice-local
// error→problem mapping functions build it; httpx only renders it, so
// the wire contract is standardized while error semantics stay with the
// owning slice.
type Problem struct {
	// Status is the HTTP status code; it also fills the "status" member
	// and, via Title, the "title" member.
	Status int
	// Type is the full problem type URI (see TypeURI).
	Type string
	// Detail is the human-readable explanation of this occurrence.
	Detail string
	// Instance (RFC 9457 §3.1.5) identifies THIS occurrence of the
	// problem. When empty, WriteProblem fills it per request with the
	// request path plus a generated occurrence id ("/path#<id>") — a
	// correlation handle the user can quote and the team can find in the
	// logs. An explicit value is rendered verbatim.
	Instance string
	// Extensions (RFC 9457 §3.2) render as additional TOP-LEVEL members,
	// keyed by member name. RFC 9457 explicitly allows extending the
	// document with problem-specific members.
	Extensions map[string]any
}

// recommendedTitles overrides http.StatusText with the phrases current
// HTTP semantics (RFC 9110, as referenced by RFC 9457) recommend. The
// notable correction: 422 is "Unprocessable Content", not the legacy
// "Unprocessable Entity" phrase Go's stdlib still emits.
var recommendedTitles = map[int]string{
	http.StatusUnprocessableEntity: "Unprocessable Content",
}

// Title derives the RFC 9457 "title" from the status code, keeping
// titles consistent API-wide with zero duplication. Current RFC
// recommendations win over legacy stdlib phrases; unknown statuses fall
// back to http.StatusText.
func (p Problem) Title() string {
	if t, ok := recommendedTitles[p.Status]; ok {
		return t
	}
	return http.StatusText(p.Status)
}

// instanceID returns a short random occurrence id for the instance
// member: 8 hex chars from crypto/rand — enough to correlate one request's
// problem with its log lines, cheap enough for every failure.
func instanceID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown" // crypto/rand unavailable: correlate by path alone
	}
	return hex.EncodeToString(b[:])
}

// GenericInternalProblem returns THE canonical generic 500 problem (S17):
// the one shape used for every failure of unknown provenance — plain
// errors reaching the renderer, and each slice's unclassified default —
// so the generic internal-error document exists exactly once in the
// codebase. It returns a fresh value: never mutate the returned Problem.
func GenericInternalProblem() Problem {
	return Problem{
		Status: http.StatusInternalServerError,
		Type:   TypeURI("internal-error"),
		Detail: "the request could not be processed at this time",
	}
}

// WriteProblem renders p as a COMPLETE RFC 9457 problem details response
// (S9, S21): Content-Type application/problem+json, the type, title,
// status, detail and instance members, plus every extension member at
// the top level. It aborts the handler chain, so a problem answer is
// always terminal. An empty Instance is filled per request (see
// Problem.Instance).
func WriteProblem(c *gin.Context, p Problem) {
	instance := p.Instance
	if instance == "" && c.Request != nil {
		instance = c.Request.URL.Path + "#" + instanceID()
	}

	// Set the media type first: gin's render only fills Content-Type
	// when unset, so this overrides the default application/json.
	c.Header("Content-Type", "application/problem+json")
	body := gin.H{
		"type":     p.Type,
		"title":    p.Title(),
		"status":   p.Status,
		"detail":   p.Detail,
		"instance": instance,
	}
	for k, v := range p.Extensions {
		body[k] = v
	}
	c.AbortWithStatusJSON(p.Status, body)
}
