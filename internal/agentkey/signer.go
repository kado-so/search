// Package agentkey owns Ed25519 management and ephemeral session signers.
// Management keys may be persisted only through keystore.Store. Session keys
// intentionally expose no persistence or marshaling API.
package agentkey

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"

	"github.com/kado-so/search/internal/keystore"
)

var (
	ErrInvalidKey         = errors.New("agent signing key is invalid")
	ErrUnsupportedHash    = errors.New("agent signing requires an unhashed message")
	ErrUnsupportedOptions = errors.New("agent signing options are unsupported")
	ErrSignerNotReady     = errors.New("agent signer is not initialized")
	ErrPersistence        = errors.New("agent management key persistence failed")
	managementKeyPrefix   = []byte{'K', 'A', 'D', 'O', 'M', 'K', 1}
)

type ed25519Signer struct {
	private ed25519.PrivateKey
}

// ManagementSigner is the long-lived installation credential.
type ManagementSigner struct {
	signer ed25519Signer
}

// SessionSigner is an ephemeral, memory-only signer. It deliberately has no
// save, marshal, seed, or private-key export method.
type SessionSigner struct {
	signer ed25519Signer
}

// GenerateManagementSigner creates a new long-lived installation credential.
func GenerateManagementSigner() (*ManagementSigner, error) {
	signer, err := generateSigner(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &ManagementSigner{signer: signer}, nil
}

// GenerateSessionSigner creates an ephemeral signer that remains memory-only.
func GenerateSessionSigner() (*SessionSigner, error) {
	signer, err := generateSigner(rand.Reader)
	if err != nil {
		return nil, err
	}
	return &SessionSigner{signer: signer}, nil
}

func generateSigner(random io.Reader) (ed25519Signer, error) {
	_, private, err := ed25519.GenerateKey(random)
	if err != nil {
		return ed25519Signer{}, fmt.Errorf("generate Ed25519 key: %w", err)
	}
	return ed25519Signer{private: private}, nil
}

// SaveManagementSigner persists the management seed through the selected key
// store. It never returns or renders private material.
func SaveManagementSigner(store keystore.Store, signer *ManagementSigner) error {
	payload, err := encodeManagementSigner(signer)
	if err != nil {
		return err
	}
	defer clear(payload)
	if store == nil {
		return ErrInvalidKey
	}
	if err := store.Save(payload); err != nil {
		return newPersistenceError(err)
	}
	return nil
}

// LoadOrCreateManagementSigner atomically retains the first management
// credential created by concurrent processes.
func LoadOrCreateManagementSigner(store keystore.Store) (*ManagementSigner, bool, error) {
	if store == nil {
		return nil, false, ErrInvalidKey
	}
	if signer, err := LoadManagementSigner(store); err == nil {
		return signer, false, nil
	} else if !errors.Is(err, keystore.ErrNotFound) {
		return nil, false, err
	}

	candidate, err := GenerateManagementSigner()
	if err != nil {
		return nil, false, err
	}
	defer clear(candidate.signer.private)
	payload, err := encodeManagementSigner(candidate)
	if err != nil {
		return nil, false, err
	}
	defer clear(payload)
	winning, created, err := store.Create(payload)
	if err != nil {
		return nil, false, newPersistenceError(err)
	}
	defer clear(winning)
	signer, err := decodeManagementSigner(winning)
	return signer, created, err
}

// LoadManagementSigner restores a previously persisted management signer.
func LoadManagementSigner(store keystore.Store) (*ManagementSigner, error) {
	if store == nil {
		return nil, ErrInvalidKey
	}
	payload, err := store.Load()
	if err != nil {
		return nil, newPersistenceError(err)
	}
	defer clear(payload)
	return decodeManagementSigner(payload)
}

func encodeManagementSigner(signer *ManagementSigner) ([]byte, error) {
	if signer == nil || len(signer.signer.private) != ed25519.PrivateKeySize {
		return nil, ErrInvalidKey
	}
	payload := make([]byte, len(managementKeyPrefix)+ed25519.SeedSize)
	copy(payload, managementKeyPrefix)
	seed := signer.signer.private.Seed()
	copy(payload[len(managementKeyPrefix):], seed)
	clear(seed)
	return payload, nil
}

func decodeManagementSigner(payload []byte) (*ManagementSigner, error) {
	if len(payload) != len(managementKeyPrefix)+ed25519.SeedSize ||
		!bytes.Equal(payload[:len(managementKeyPrefix)], managementKeyPrefix) {
		return nil, ErrInvalidKey
	}
	seed := make([]byte, ed25519.SeedSize)
	copy(seed, payload[len(managementKeyPrefix):])
	private := ed25519.NewKeyFromSeed(seed)
	clear(seed)
	return &ManagementSigner{signer: ed25519Signer{private: private}}, nil
}

func (signer *ManagementSigner) Public() crypto.PublicKey {
	if signer == nil {
		return ed25519.PublicKey(nil)
	}
	return signer.signer.publicKey()
}

func (signer *ManagementSigner) Sign(
	random io.Reader,
	message []byte,
	options crypto.SignerOpts,
) ([]byte, error) {
	if signer == nil {
		return nil, ErrSignerNotReady
	}
	return signer.signer.sign(random, message, options)
}

func (signer ManagementSigner) String() string {
	return "Ed25519 management signer [redacted]"
}

func (signer ManagementSigner) GoString() string {
	return "agentkey.ManagementSigner{redacted}"
}

func (signer *SessionSigner) Public() crypto.PublicKey {
	if signer == nil {
		return ed25519.PublicKey(nil)
	}
	return signer.signer.publicKey()
}

func (signer *SessionSigner) Sign(
	random io.Reader,
	message []byte,
	options crypto.SignerOpts,
) ([]byte, error) {
	if signer == nil {
		return nil, ErrSignerNotReady
	}
	return signer.signer.sign(random, message, options)
}

func (signer SessionSigner) String() string {
	return "Ed25519 session signer [redacted]"
}

func (signer SessionSigner) GoString() string {
	return "agentkey.SessionSigner{redacted}"
}

func (signer ed25519Signer) publicKey() ed25519.PublicKey {
	if len(signer.private) != ed25519.PrivateKeySize {
		return nil
	}
	public := signer.private.Public().(ed25519.PublicKey)
	return append(ed25519.PublicKey(nil), public...)
}

func (signer ed25519Signer) sign(
	_ io.Reader,
	message []byte,
	options crypto.SignerOpts,
) ([]byte, error) {
	if len(signer.private) != ed25519.PrivateKeySize {
		return nil, ErrSignerNotReady
	}
	if options != nil && options.HashFunc() != crypto.Hash(0) {
		return nil, ErrUnsupportedHash
	}
	if ed25519Options, ok := options.(*ed25519.Options); ok && ed25519Options.Context != "" {
		return nil, ErrUnsupportedOptions
	}
	signature := ed25519.Sign(signer.private, message)
	return append([]byte(nil), signature...), nil
}

type persistenceError struct {
	details *persistenceErrorDetails
}

type persistenceErrorDetails struct {
	cause error
}

func newPersistenceError(cause error) *persistenceError {
	return &persistenceError{details: &persistenceErrorDetails{cause: cause}}
}

func (persistenceError) Error() string {
	return ErrPersistence.Error()
}

func (err persistenceError) String() string {
	return err.Error()
}

func (persistenceError) GoString() string {
	return "agentkey.persistenceError{redacted}"
}

func (err persistenceError) Format(state fmt.State, verb rune) {
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

func (err *persistenceError) Unwrap() []error {
	if err == nil || err.details == nil {
		return []error{ErrPersistence}
	}
	return []error{ErrPersistence, err.details.cause}
}
