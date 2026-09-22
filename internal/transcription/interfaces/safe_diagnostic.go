package interfaces

import "errors"

// SafeRuntimeDiagnostic contains only application-owned text. Construct it from
// a fixed diagnostic catalog, never third-party exception messages, stderr,
// input paths, prompts, transcripts, or credentials. Cause remains unwrap-only.
type SafeRuntimeDiagnostic struct {
	code, message string
	cause         error
}

func NewSafeRuntimeDiagnostic(code, message string, cause error) *SafeRuntimeDiagnostic {
	return &SafeRuntimeDiagnostic{code: code, message: message, cause: cause}
}
func (e *SafeRuntimeDiagnostic) Error() string { return e.message }
func (e *SafeRuntimeDiagnostic) Unwrap() error { return e.cause }
func (e *SafeRuntimeDiagnostic) Code() string  { return e.code }

func RuntimeDiagnostic(err error) (*SafeRuntimeDiagnostic, bool) {
	var diagnostic *SafeRuntimeDiagnostic
	ok := errors.As(err, &diagnostic)
	return diagnostic, ok
}
