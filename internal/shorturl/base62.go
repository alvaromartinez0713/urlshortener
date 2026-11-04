package shorturl

// base62Alphabet is the code alphabet for Base62: digits, then uppercase,
// then lowercase (0-9, A-Z, a-z). Base 62. Order is part of the public
// behavior pinned by Base62Encode tests — do not reorder.
const base62Alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// base62Base is the radix of the alphabet.
const base62Base = uint64(len(base62Alphabet))

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
