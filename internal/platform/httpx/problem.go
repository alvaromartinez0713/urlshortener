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
}

// Title derives the RFC 9457 "title" from the status code, keeping
// titles consistent API-wide with zero duplication.
func (p Problem) Title() string {
	return http.StatusText(p.Status)
}

// WriteProblem renders p as a COMPLETE RFC 9457 problem details response
// (S9): Content-Type application/problem+json plus the type, title,
// status and detail members. It aborts the handler chain, so a problem
// answer is always terminal.
func WriteProblem(c *gin.Context, p Problem) {
	// Set the media type first: gin's render only fills Content-Type
	// when unset, so this overrides the default application/json.
	c.Header("Content-Type", "application/problem+json")
	c.AbortWithStatusJSON(p.Status, gin.H{
		"type":   p.Type,
		"title":  p.Title(),
		"status": p.Status,
		"detail": p.Detail,
	})
}
