package shorturl

import (
	"fmt"
	"net/url"
	"strings"
)

// ShortURL is the aggregate of the shorten slice: the mapping between a
// short code and the target (long) URL.
//
// Rich domain (S7): the only way to build one is NewShortURL, which
// applies validation (this file) and code assignment (Base62 of the
// sequence from the CodeGenerator port). There are no exported fields and
// no setters — accessors keep the entity immutable after construction, so
// no external code can desynchronize code and target (the mapping the
// whole system exists to preserve).
type ShortURL struct {
	target string
	code   string
}

// NewShortURL validates the long URL and builds a ShortURL with a code
// derived from the next sequence of gen (S2, S3, S7).
//
// Ordering guarantee: the URL is fully validated BEFORE the sequence is
// consumed, so rejected input (S2: "rejected input must not persist
// anything") never burns a sequence value and never produces a ShortURL.
//
// It returns one of the domain errors in this package for invalid input
// (matched via errors.Is), or the CodeGenerator's own error if the
// sequence source fails. On any error the returned ShortURL is the zero
// value.
func NewShortURL(longURL string, gen CodeGenerator) (ShortURL, error) {
	target, err := validateLongURL(longURL)
	if err != nil {
		return ShortURL{}, err
	}

	sequence, err := gen.NextSequence()
	if err != nil {
		return ShortURL{}, fmt.Errorf("shorturl: code generation failed: %w", err)
	}

	return ShortURL{target: target, code: Base62Encode(sequence)}, nil
}

// Code returns the short code for this URL (e.g. "10" for a base62
// sequence of 62). Immutable after construction.
func (s ShortURL) Code() string { return s.code }

// Target returns the validated long URL this code redirects to.
// Immutable after construction.
func (s ShortURL) Target() string { return s.target }

// validateLongURL applies the v1 URL rules (S2). Rules, minimal and
// documented:
//
//   - non-empty after trimming surrounding whitespace (users paste URLs
//     with stray spaces; trimming here is input hygiene, not business
//     mutation — the stored target keeps its trimmed form);
//   - must parse as an absolute URL (net/url.Parse);
//   - scheme must be http or https — anything else (ftp, javascript,
//     data, ...) is rejected outright: v1 codes are meant to be visited
//     in browsers, and javascript:/data: URLs are the classic phishing
//     vector for a redirector;
//   - host must be non-empty (http:// is not a usable target);
//   - userinfo (http://user:pass@host) is rejected: it exists to confuse
//     viewers about the real destination (evil@example.com tricks) and
//     has no legitimate v1 use. This is the one deliberate extra rule.
//
// Fragments and query strings are preserved as-is.
func validateLongURL(longURL string) (string, error) {
	trimmed := strings.TrimSpace(longURL)
	if trimmed == "" {
		return "", ErrEmptyURL
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("%w: %q is not a parseable url", ErrInvalidURL, trimmed)
	}

	switch {
	case parsed.Scheme == "":
		return "", fmt.Errorf("%w: %q has no scheme", ErrInvalidURL, trimmed)
	case parsed.Scheme != "http" && parsed.Scheme != "https":
		return "", fmt.Errorf("%w: %q (scheme %q)", ErrInvalidURLScheme, trimmed, parsed.Scheme)
	case parsed.Host == "":
		return "", fmt.Errorf("%w: %q has no host", ErrInvalidURL, trimmed)
	case parsed.User != nil:
		return "", fmt.Errorf("%w: %q contains userinfo", ErrInvalidURL, trimmed)
	}

	return trimmed, nil
}
