package httpx

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// S13 tests for ProblemError: it is an error whose message is the
// user-safe mapped detail, while the internal cause travels only via
// Unwrap — for logs, never for the response body.

func TestProblemErrorIsErrorWithUserSafeMessage(t *testing.T) {
	cause := errors.New(`pq: password authentication failed for user "app" on db.internal:5432`)
	pe := NewProblemError(Problem{
		Status: 500,
		Type:   TypeURI("internal-error"),
		Detail: "the request could not be processed at this time",
	}, cause)

	var err error = pe
	if err.Error() != "the request could not be processed at this time" {
		t.Errorf("Error() = %q, want exactly the user-safe mapped detail", err.Error())
	}
	if strings.Contains(err.Error(), "password authentication") {
		t.Errorf("Error() leaked the internal cause text: %q", err.Error())
	}
}

func TestProblemErrorUnwrapExposesCause(t *testing.T) {
	cause := errors.New("connection refused")
	pe := NewProblemError(Problem{
		Status: 500,
		Type:   TypeURI("internal-error"),
		Detail: "the request could not be processed at this time",
	}, cause)

	if !errors.Is(pe, cause) {
		t.Error("errors.Is(pe, cause) = false, want true (Unwrap must expose the cause for the error log)")
	}
}

func TestProblemErrorCarriesProblem(t *testing.T) {
	p := Problem{Status: 422, Type: TypeURI("invalid-url"), Detail: "url must be absolute"}
	pe := NewProblemError(p, errors.New("url must be absolute"))

	// DeepEqual (not ==): Problem carries an extensions map (S21), which
	// is not comparable with the == operator.
	if !reflect.DeepEqual(pe.Problem, p) {
		t.Errorf("pe.Problem = %+v, want %+v", pe.Problem, p)
	}
}
