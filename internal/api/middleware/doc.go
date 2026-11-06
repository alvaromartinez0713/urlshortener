// Package middleware holds the api layer's cross-cutting HTTP middleware
// (S13): the error handler — the SINGLE place that renders error responses
// and logs them — and the slog request logger that replaced gin.Logger.
//
// It lives in its own subpackage of internal/api, not in the api package
// itself, so slice-internal tests (package shorten / package redirect)
// can wire the REAL middleware into their handler-test routers without an
// import cycle (api imports the slices; the slices' tests import this
// package, which imports nothing slice-owned).

package middleware
