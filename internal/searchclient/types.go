// Package searchclient implements the authenticated Kado Search HTTP lifecycle.
package searchclient

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/kado-so/search/internal/diagnostic"
	"github.com/kado-so/search/internal/searchcontract"
)

const (
	// CanonicalMediaType is the versioned Phase 03A Search Document media type.
	CanonicalMediaType = "application/vnd.kado.search.v1+json"
	SchemaVersion      = searchcontract.SchemaVersion
)

const (
	StatusQueued     = "queued"
	StatusRunning    = "running"
	StatusNeedsInput = "needs_input"
	StatusComplete   = "complete"
	StatusFailed     = "failed"
	StatusCanceled   = "canceled"
)

var (
	ErrProtocol           = errors.New("search response protocol failed")
	ErrAuthentication     = errors.New("search authentication failed")
	ErrNeedsInput         = errors.New("search needs input")
	ErrFailed             = errors.New("search failed")
	ErrCanceled           = errors.New("search canceled")
	ErrTimeout            = errors.New("search timed out")
	ErrRedirect           = errors.New("search redirect rejected")
	ErrUnsupportedVersion = errors.New(
		"search document major version is not supported",
	)
)

// AuthorizationSource returns an in-memory Phase 02C bearer authorization
// value. A true refresh value is used only after an authenticated request was
// rejected before the Search operation ran.
type AuthorizationSource interface {
	Authorization(context.Context, bool) (string, error)
}

// Limits bounds remote input, retries, lifecycle work, pages, and request
// duration. Lifecycle operations include the initial Search plus status,
// clarification, and retry requests; pagination has its own independent bound.
type Limits struct {
	MaxDocumentBytes       int64
	MaxErrorBytes          int64
	MaxPages               int
	MaxGETAttempts         int
	MaxLifecycleOperations int
	MaxClarifications      int
	MaxRequestTime         time.Duration
}

func DefaultLimits() Limits {
	return Limits{
		MaxDocumentBytes:       4 * 1024 * 1024,
		MaxErrorBytes:          64 * 1024,
		MaxPages:               100,
		MaxGETAttempts:         2,
		MaxLifecycleOperations: 256,
		MaxClarifications:      8,
		MaxRequestTime:         30 * time.Second,
	}
}

// Question is the bounded clarification request in a needs_input document.
type Question struct {
	ID      string
	Prompt  string
	Options []string
}

// Failure is the bounded public failure in a failed Search Document.
type Failure struct {
	Code      string
	Message   string
	Retryable bool
}

// Document preserves the exact response bytes after full Search Document v1
// schema, JSON-LD, and semantic validation while exposing stable lifecycle
// fields used by the lifecycle client.
type Document struct {
	raw           []byte
	self          *url.URL
	next          *url.URL
	previous      *url.URL
	SchemaVersion string
	SearchID      string
	Query         string
	Status        string
	Question      *Question
	Failure       *Failure
}

// Bytes returns an isolated copy of the canonical server document.
func (document Document) Bytes() []byte {
	return append([]byte(nil), document.raw...)
}

// HasNext reports whether the document includes a server-provided next page.
func (document Document) HasNext() bool {
	return document.next != nil
}

// Result is a terminal lifecycle result and any pages followed from the first
// completed document. Documents remain separate canonical representations.
type Result struct {
	Document Document
	Pages    []Document
}

// Clarifier supplies an answer for a bounded server question. It must not
// receive credentials or raw response bytes.
type Clarifier func(context.Context, Question) (string, error)

// RunOptions controls lifecycle behavior without changing the server contract.
type RunOptions struct {
	Timeout         time.Duration
	PollInterval    time.Duration
	Clarify         Clarifier
	FollowPages     bool
	RetryFailure    bool
	CancelOnTimeout bool
}

func DefaultRunOptions() RunOptions {
	return RunOptions{
		Timeout:         2 * time.Minute,
		PollInterval:    time.Second,
		FollowPages:     true,
		CancelOnTimeout: true,
	}
}

// Error is safe for ordinary diagnostics. Its private cause and remote body
// are never included in Error, String, or GoString output.
type Error struct {
	code       string
	message    string
	status     int
	retryable  bool
	privateErr error
}

func newError(code, message string, status int, retryable bool, cause error) *Error {
	return &Error{
		code:       boundedCode(code),
		message:    diagnostic.TerminalSafeText(message, 280),
		status:     status,
		retryable:  retryable,
		privateErr: cause,
	}
}

func (failure *Error) Error() string {
	if failure.message == "" {
		return "Search request failed."
	}
	return failure.message
}

func (failure *Error) String() string {
	return failure.Error()
}

func (*Error) GoString() string {
	return "searchclient.Error{redacted}"
}

func (failure *Error) Unwrap() error {
	return failure.privateErr
}

func (failure *Error) Code() string {
	return failure.code
}

func (failure *Error) StatusCode() int {
	return failure.status
}

func (failure *Error) Retryable() bool {
	return failure.retryable
}

// NeedsInputError is returned when the server asks a question and no
// clarifier was configured.
type NeedsInputError struct {
	Question Question
}

func (*NeedsInputError) Error() string    { return "Search requires clarification." }
func (*NeedsInputError) GoString() string { return "searchclient.NeedsInputError{redacted}" }
func (*NeedsInputError) Unwrap() error    { return ErrNeedsInput }

// FailureError reports a structured terminal Search failure.
type FailureError struct {
	Failure Failure
}

func (failure *FailureError) Error() string {
	return failure.Failure.Message
}

func (*FailureError) GoString() string { return "searchclient.FailureError{redacted}" }
func (*FailureError) Unwrap() error    { return ErrFailed }

// CanceledError reports a terminal canceled lifecycle.
type CanceledError struct{}

func (*CanceledError) Error() string    { return "Search was canceled." }
func (*CanceledError) GoString() string { return "searchclient.CanceledError{}" }
func (*CanceledError) Unwrap() error    { return ErrCanceled }

// TimeoutError reports that the local lifecycle deadline elapsed.
type TimeoutError struct{}

func (*TimeoutError) Error() string    { return "Search did not complete before the timeout." }
func (*TimeoutError) GoString() string { return "searchclient.TimeoutError{}" }
func (*TimeoutError) Unwrap() error    { return ErrTimeout }

func (document Document) String() string {
	return fmt.Sprintf("Search status=%s id=%s", document.Status, document.SearchID)
}

func (Document) GoString() string {
	return "searchclient.Document{canonical bytes redacted}"
}
