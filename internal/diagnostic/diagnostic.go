// Package diagnostic defines errors that are safe to print in ordinary CLI
// output. Private causes remain available to trusted callers but are never
// rendered by this package.
package diagnostic

import (
	"errors"
	"strings"
	"unicode/utf8"
)

const (
	// ExitFailure is used for an operational failure.
	ExitFailure = 1
	// ExitUsage is used for invalid command-line input.
	ExitUsage = 2

	maxCodeBytes    = 48
	maxMessageBytes = 256
)

const (
	fallbackCode    = "unexpected_error"
	fallbackMessage = "an unexpected error occurred"
)

// Error contains a stable code and bounded public message plus an optional
// private cause. Error deliberately returns only the public message.
type Error struct {
	code     string
	message  string
	exitCode int
	cause    error
}

// New constructs a diagnostic. Control characters and excessive content are
// removed from values that can reach ordinary output.
func New(code, message string, exitCode int, cause error) *Error {
	code = boundedCode(code)
	message = bounded(message, maxMessageBytes)
	if code == "" {
		code = fallbackCode
	}
	if message == "" {
		message = fallbackMessage
	}
	if exitCode != ExitUsage {
		exitCode = ExitFailure
	}
	return &Error{code: code, message: message, exitCode: exitCode, cause: cause}
}

func (err *Error) Error() string {
	return err.message
}

func (err *Error) Unwrap() error {
	return err.cause
}

// Code returns the stable machine-readable diagnostic code.
func (err *Error) Code() string {
	return err.code
}

// ExitCode returns the process exit status assigned to the diagnostic.
func (err *Error) ExitCode() int {
	return err.exitCode
}

// Public converts any error to printable fields. Unknown errors fail closed to
// a generic diagnostic rather than exposing their contents.
func Public(err error) (code, message string, exitCode int) {
	var safe *Error
	if errors.As(err, &safe) {
		return safe.Code(), safe.Error(), safe.ExitCode()
	}
	return fallbackCode, fallbackMessage, ExitFailure
}

func boundedCode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	output := make([]rune, 0, min(len([]rune(value)), maxCodeBytes))
	for _, character := range value {
		if len(output) == maxCodeBytes {
			break
		}
		switch {
		case character >= 'a' && character <= 'z':
			output = append(output, character)
		case character >= '0' && character <= '9':
			output = append(output, character)
		case character == '_':
			output = append(output, character)
		default:
			output = append(output, '_')
		}
	}
	return strings.Trim(string(output), "_")
}

func bounded(value string, maximum int) string {
	value = strings.TrimSpace(value)
	output := make([]rune, 0, min(len(value), maximum))
	byteCount := 0
	for _, character := range value {
		if character < ' ' || character == '\u007f' {
			character = ' '
		}
		characterBytes := utf8.RuneLen(character)
		if byteCount+characterBytes > maximum {
			break
		}
		output = append(output, character)
		byteCount += characterBytes
	}
	return strings.Join(strings.Fields(string(output)), " ")
}
