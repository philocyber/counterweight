package locale

import (
	"errors"
	"fmt"
	"strings"
)

// messageError retains a built-in template and its raw arguments until display.
// Error is stable English for logs/comparisons; Message renders at the request
// boundary without searching or rewriting text inside arguments or evidence.
type messageError struct {
	template string
	args     []any
	original error
}

func (e *messageError) Error() string { return e.original.Error() }
func (e *messageError) Unwrap() error { return e.original }

// Errorf creates a localizable error while preserving fmt.Errorf wrapping.
func Errorf(english string, args ...any) error {
	return &messageError{english, append([]any(nil), args...), fmt.Errorf(english, args...)}
}

// NewError creates an explicitly localizable built-in error, not a user string.
func NewError(english string) error {
	return &messageError{template: english, original: errors.New(english)}
}

// ErrorMessage translates only errors created by Errorf/NewError. Unknown
// errors remain verbatim. It never pattern-matches a formatted error string.
func ErrorMessage(lang Lang, err error) string {
	if err == nil {
		return ""
	}
	if message, ok := err.(interface{ MessageForLanguage(Lang) string }); ok {
		return message.MessageForLanguage(lang)
	}
	if joined, ok := err.(interface{ Unwrap() []error }); ok {
		var original, translated []string
		for _, cause := range joined.Unwrap() {
			if cause != nil {
				original = append(original, cause.Error())
				translated = append(translated, ErrorMessage(lang, cause))
			}
		}
		// Preserve custom multi-error wrappers with their own explanatory text.
		if err.Error() == strings.Join(original, "\n") {
			return strings.Join(translated, "\n")
		}
	}
	e, ok := err.(*messageError)
	if !ok {
		return err.Error()
	}
	template, _ := Lookup(lang, e.template)
	if len(e.args) == 0 {
		return template
	}
	args := append([]any(nil), e.args...)
	for i, arg := range args {
		if cause, ok := arg.(error); ok {
			args[i] = errors.New(ErrorMessage(lang, cause))
		}
	}
	return fmt.Errorf(template, args...).Error()
}
