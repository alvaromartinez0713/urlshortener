// Package shorturl is the business core of the URL shortener slice.
//
// Architecture contract (S5): this package is pure Go stdlib — it imports
// no Gin, no pgx, no database/sql, no net/http, and no clock. Every
// external interaction (sequence source, persistence) crosses a
// core-owned port.
package shorturl

import "errors"

// Domain errors are values (S5.2/S7): callers match with errors.Is, never
// by string comparison, so problem-detail mapping in the API layer (S8)
// stays deterministic.
var (
	// ErrEmptyURL is returned when the long URL is empty or whitespace.
	ErrEmptyURL = errors.New("shorturl: long url is empty")
	// ErrInvalidURL is returned when the long URL does not parse as an
	// absolute URL with a host (e.g. missing scheme, malformed, no host).
	ErrInvalidURL = errors.New("shorturl: invalid url")
	// ErrInvalidURLScheme is returned when the URL parses but its scheme
	// is neither http nor https.
	ErrInvalidURLScheme = errors.New("shorturl: only http and https schemes are allowed")
	// ErrCorruptStoredShortURL is returned by NewShortURLFromRepository
	// when the stored row is missing its code or target — something only
	// a corrupted or manually tampered row can produce (the schema's NOT
	// NULL constraints guard the normal write path). It signals a data
	// integrity problem, not invalid user input.
	ErrCorruptStoredShortURL = errors.New("shorturl: stored short url is missing its code or target")
)
