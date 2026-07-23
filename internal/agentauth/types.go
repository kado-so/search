// Package agentauth implements discovery, admission, ephemeral sessions, and
// short-lived OAuth tokens for autonomous Kado principals.
package agentauth

import (
	"errors"
	"fmt"
	"io"
	"time"
)

const (
	ProtocolVersion     = "0.1"
	ProofAlgorithm      = "urn:agent-principal:challenge:argon2id-deterministic-v1"
	enrollmentProofType = "agent-enrollment+jws"
	enrollmentOperation = "authenticate-or-enroll"
)

var (
	ErrDiscovery          = errors.New("agent authentication discovery failed")
	ErrRedirect           = errors.New("agent authentication redirect rejected")
	ErrProtocol           = errors.New("agent authentication protocol failed")
	ErrCredentialNotFound = errors.New("agent management credential not found")
	ErrAgentNotFound      = errors.New("agent principal not found")
	ErrAdmissionRequired  = errors.New("agent enrollment admission proof required")
	ErrChallengeLimits    = errors.New("agent admission challenge exceeds client limits")
	ErrChallengeExpired   = errors.New("agent admission challenge expired")
	ErrAuthentication     = errors.New("agent authentication failed")
)

type EnrollmentMode uint8

const (
	AuthenticateOnly EnrollmentMode = iota
	CreateIfMissing
)

type Limits struct {
	MaxResponseBytes       int64
	MaxClockSkew           time.Duration
	MaxProofLifetime       time.Duration
	MaxHTTPTimeout         time.Duration
	MaxChallengeLifetime   time.Duration
	MaxArgonMemoryKiB      uint32
	MaxArgonPasses         uint32
	MaxArgonParallelism    uint8
	MaxArgonAttempts       uint32
	MaxArgonElapsed        time.Duration
	MaxSessionLifetime     time.Duration
	MaxAccessTokenLifetime time.Duration
}

func DefaultLimits() Limits {
	return Limits{
		MaxResponseBytes:       256 * 1024,
		MaxClockSkew:           30 * time.Second,
		MaxProofLifetime:       60 * time.Second,
		MaxHTTPTimeout:         30 * time.Second,
		MaxChallengeLifetime:   10 * time.Minute,
		MaxArgonMemoryKiB:      128 * 1024,
		MaxArgonPasses:         4,
		MaxArgonParallelism:    4,
		MaxArgonAttempts:       10_000,
		MaxArgonElapsed:        2 * time.Minute,
		MaxSessionLifetime:     24 * time.Hour,
		MaxAccessTokenLifetime: 5 * time.Minute,
	}
}

type Metadata struct {
	Resource             string
	Issuer               string
	TokenEndpoint        string
	JWKSURI              string
	NonceEndpoint        string
	EnrollmentEndpoint   string
	AdmissionEndpoint    string
	CredentialEndpoint   string
	AgentMetadataURI     string
	AutonomousEnrollment bool
}

type Request struct {
	Mode EnrollmentMode
}

type Result struct {
	Created                 bool
	PrincipalID             string
	CredentialID            string
	ClientID                string
	TokenEndpoint           string
	TokenEndpointAuthMethod string
}

func (result Result) String() string {
	return fmt.Sprintf(
		"agent authentication result principal=%s credential=%s created=%t",
		result.PrincipalID,
		result.CredentialID,
		result.Created,
	)
}

func (Result) GoString() string {
	return "agentauth.Result{safe identifiers}"
}

// SessionToken is an in-memory autonomous-agent session and its short-lived
// bearer credential. The raw token is intentionally not exported or formatted.
type SessionToken struct {
	PrincipalID         string
	CredentialID        string
	ClientID            string
	SessionID           string
	SessionCredentialID string
	SessionExpiresAt    time.Time
	AccessExpiresAt     time.Time
	Scopes              []string
	accessToken         string
}

// AuthorizationHeader returns the value needed for an HTTP Authorization
// header. Callers must not print or persist it.
func (token SessionToken) AuthorizationHeader() string {
	if token.accessToken == "" {
		return ""
	}
	return "Bearer " + token.accessToken
}

func (token SessionToken) String() string {
	return fmt.Sprintf(
		"agent session token principal=%s session=%s expires=%s",
		token.PrincipalID,
		token.SessionID,
		token.AccessExpiresAt.UTC().Format(time.RFC3339),
	)
}

func (SessionToken) GoString() string {
	return "agentauth.SessionToken{safe identifiers, token:[redacted]}"
}

type protocolError struct {
	details *protocolErrorDetails
}

type protocolErrorDetails struct {
	kind  error
	cause error
}

func newProtocolError(kind, cause error) error {
	return &protocolError{details: &protocolErrorDetails{kind: kind, cause: cause}}
}

func (err protocolError) Error() string {
	if err.details == nil || err.details.kind == nil {
		return ErrProtocol.Error()
	}
	return err.details.kind.Error()
}

func (err protocolError) String() string {
	return err.Error()
}

func (protocolError) GoString() string {
	return "agentauth.protocolError{redacted}"
}

func (err protocolError) Format(state fmt.State, verb rune) {
	rendered := err.Error()
	if verb == 'v' && state.Flag('#') {
		rendered = err.GoString()
	}
	if verb == 'q' {
		_, _ = fmt.Fprintf(state, "%q", rendered)
		return
	}
	_, _ = io.WriteString(state, rendered)
}

func (err *protocolError) Is(target error) bool {
	return err != nil && err.details != nil && target == err.details.kind
}

func (err *protocolError) Unwrap() error {
	if err == nil || err.details == nil {
		return nil
	}
	return err.details.cause
}
