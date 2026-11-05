package shorturl

import (
	"strings"
)

// base62Alphabet is the code alphabet for Base62: digits, then uppercase,
// then lowercase (0-9, A-Z, a-z). Base 62. Order is part of the public
// behavior pinned by Base62Encode tests — do not reorder.
const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// base62Base is the radix of the alphabet.
const base62Base = uint64(len(base62Alphabet))

// IsPossibleCode reports whether code could have been produced by
// Base62Encode: non-empty and entirely over base62Alphabet (S16).
//
// It is a DOMAIN predicate (S16): the alphabet is the domain's vocabulary
// — Base62Encode defines it — so the rule "could this string ever have
// been a code?" is answered here, once, by the package that owns the
// alphabet. Consumers (e.g. the redirect slice's cheap-rejection fast
// path) call it instead of duplicating the alphabet and cross-checking
// against drift.
func IsPossibleCode(code string) bool {
	if code == "" {
		return false
	}
	for i := 0; i < len(code); i++ {
		if !strings.ContainsRune(base62Alphabet, rune(code[i])) {
			return false
		}
	}
	return true
}

// Base62Encode renders a sequence value as a Base62 string over
// base62Alphabet.
//
// Encoding choice, documented: Base62Encode(0) returns "0" — a non-empty
// single-character code. An empty string would force callers to special
// case zero and would make an empty code a valid (and confusing) URL
// suffix.
//
// This is a pure function of its input: no randomness, no clock, no I/O
// (S5.1). The same sequence value always yields the same code, which is
// what makes the S3 uniqueness story (sequence + unique index) sound.
func Base62Encode(value uint64) string {
	if value == 0 {
		return string(base62Alphabet[0])
	}

	digits := make([]byte, 0, 11) // max uint64 needs 11 base62 digits
	for value > 0 {
		digits = append(digits, base62Alphabet[value%base62Base])
		value /= base62Base
	}

	// Digits were produced least-significant first; reverse in place.
	for i, j := 0, len(digits)-1; i < j; i, j = i+1, j-1 {
		digits[i], digits[j] = digits[j], digits[i]
	}
	return string(digits)
}
