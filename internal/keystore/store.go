// Package keystore stores long-lived agent key material without exposing it in
// ordinary output. The operating-system keychain is the preferred adapter.
package keystore

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	recordVersion       = 1
	maxKeyMaterialBytes = 4 * 1024
	maxEncodedBytes     = 8 * 1024
)

var (
	ErrNotFound    = errors.New("key material not found")
	ErrCorrupt     = errors.New("key material is corrupt")
	ErrInvalid     = errors.New("key material is invalid")
	ErrPermissions = errors.New("key storage permissions are unsafe")
	ErrUnavailable = errors.New("key storage is unavailable")
	ErrUnsupported = errors.New("key storage is unsupported")
)

// Store persists one opaque long-lived key payload.
type Store interface {
	Load() ([]byte, error)
	Save(keyMaterial []byte) error
	Delete() error
}

// Error retains a private cause for trusted diagnostics while rendering only a
// bounded operation and safe classification through Error().
type Error struct {
	details *errorDetails
}

type errorDetails struct {
	operation string
	kind      error
	cause     error
}

func (err Error) Error() string {
	if err.details == nil {
		return "key storage operation failed"
	}
	return fmt.Sprintf("key storage %s failed: %s", err.details.operation, err.details.kind)
}

func (err Error) String() string {
	return err.Error()
}

func (err Error) GoString() string {
	return "keystore.Error{redacted}"
}

func (err Error) Format(state fmt.State, verb rune) {
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

func (err *Error) Unwrap() error {
	if err == nil || err.details == nil {
		return nil
	}
	return err.details.cause
}

func (err *Error) Is(target error) bool {
	return err != nil && err.details != nil && target == err.details.kind
}

func storageError(operation string, kind, cause error) error {
	return &Error{details: &errorDetails{operation: operation, kind: kind, cause: cause}}
}

type record struct {
	Version int    `json:"version"`
	Data    string `json:"data"`
}

func encodeRecord(keyMaterial []byte) (string, error) {
	if len(keyMaterial) == 0 || len(keyMaterial) > maxKeyMaterialBytes {
		return "", storageError("encode", ErrInvalid, nil)
	}
	encoded, err := json.Marshal(record{
		Version: recordVersion,
		Data:    base64.RawStdEncoding.EncodeToString(keyMaterial),
	})
	if err != nil {
		return "", storageError("encode", ErrInvalid, err)
	}
	return string(encoded), nil
}

func decodeRecord(encoded string) ([]byte, error) {
	if len(encoded) == 0 || len(encoded) > maxEncodedBytes {
		return nil, storageError("decode", ErrCorrupt, nil)
	}
	decoder := json.NewDecoder(bytes.NewBufferString(encoded))
	decoder.DisallowUnknownFields()
	var stored record
	if err := decoder.Decode(&stored); err != nil {
		return nil, storageError("decode", ErrCorrupt, err)
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return nil, storageError("decode", ErrCorrupt, nil)
	}
	if stored.Version != recordVersion || stored.Data == "" {
		return nil, storageError("decode", ErrCorrupt, nil)
	}
	keyMaterial, err := base64.RawStdEncoding.Strict().DecodeString(stored.Data)
	if err != nil || len(keyMaterial) == 0 || len(keyMaterial) > maxKeyMaterialBytes {
		return nil, storageError("decode", ErrCorrupt, err)
	}
	return keyMaterial, nil
}
