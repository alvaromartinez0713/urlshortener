package shorturl

// CodeGenerator is the core-owned port for code generation (S5.2: every
// external interaction crosses a port). The domain decides WHAT a code is
// (Base62 of a sequence value, see Base62Encode); the adapter decides
// WHERE the sequence comes from (e.g. a Postgres bigserial).
//
// NextSequence returns the next unique sequence value, or an error if the
// sequence source is unavailable. It returns the error as produced by the
// adapter: the domain does not wrap infrastructure failures into domain
// error values.
//
// Collision retry (S3: unique index on code, retry on collision) is
// deliberately NOT part of the domain: detecting a collision requires the
// repository. The use case (T4) owns the retry loop — it can call
// NextSequence again and re-invoke NewShortURL. This design accommodates
// that without change.
type CodeGenerator interface {
	NextSequence() (uint64, error)
}
