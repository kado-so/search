package agentkey

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"encoding"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/diagnostic"
	"github.com/kado-so/search/internal/keystore"
)

type memoryStore struct {
	payload   []byte
	loadError error
	saveError error
}

type verbosePrivateError struct {
	Secret string
}

func (verbosePrivateError) Error() string {
	return "private persistence failure"
}

func (store *memoryStore) Load() ([]byte, error) {
	if store.loadError != nil {
		return nil, store.loadError
	}
	if store.payload == nil {
		return nil, keystore.ErrNotFound
	}
	return append([]byte(nil), store.payload...), nil
}

func (store *memoryStore) Save(payload []byte) error {
	if store.saveError != nil {
		return store.saveError
	}
	store.payload = append([]byte(nil), payload...)
	return nil
}

func (store *memoryStore) Delete() error {
	if store.payload == nil {
		return keystore.ErrNotFound
	}
	clear(store.payload)
	store.payload = nil
	return nil
}

func deterministicManagementSigner(t *testing.T) *ManagementSigner {
	t.Helper()
	signer, err := generateSigner(bytes.NewReader(bytes.Repeat([]byte{0xA5}, ed25519.SeedSize)))
	if err != nil {
		t.Fatalf("generateSigner() error = %v", err)
	}
	return &ManagementSigner{signer: signer}
}

func TestManagementSignerPersistsAndSignsAcrossLoads(t *testing.T) {
	t.Parallel()

	store := &memoryStore{}
	created := deterministicManagementSigner(t)
	if err := SaveManagementSigner(store, created); err != nil {
		t.Fatalf("SaveManagementSigner() error = %v", err)
	}
	loaded, err := LoadManagementSigner(store)
	if err != nil {
		t.Fatalf("LoadManagementSigner() error = %v", err)
	}

	createdPublic := created.Public().(ed25519.PublicKey)
	loadedPublic := loaded.Public().(ed25519.PublicKey)
	if !bytes.Equal(createdPublic, loadedPublic) {
		t.Fatal("loaded management identity differs")
	}

	message := []byte("canonical management assertion bytes")
	signature, err := loaded.Sign(nil, message, crypto.Hash(0))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	if !ed25519.Verify(loadedPublic, message, signature) {
		t.Fatal("signature did not verify")
	}
	if ed25519.Verify(loadedPublic, []byte("tampered"), signature) {
		t.Fatal("signature verified a tampered message")
	}
}

func TestManagementSignerRejectsCorruptOrUnknownKeyFormats(t *testing.T) {
	t.Parallel()

	for _, payload := range [][]byte{
		nil,
		[]byte("not-a-key"),
		append([]byte{'K', 'A', 'D', 'O', 'M', 'K', 2}, make([]byte, ed25519.SeedSize)...),
		append(append([]byte(nil), managementKeyPrefix...), make([]byte, ed25519.SeedSize-1)...),
	} {
		store := &memoryStore{payload: append([]byte(nil), payload...)}
		_, err := LoadManagementSigner(store)
		if payload == nil {
			if !errors.Is(err, ErrPersistence) || !errors.Is(err, keystore.ErrNotFound) {
				t.Errorf("LoadManagementSigner(nil) error = %v", err)
			}
			continue
		}
		if !errors.Is(err, ErrInvalidKey) {
			t.Errorf("LoadManagementSigner(corrupt) error = %v, want ErrInvalidKey", err)
		}
	}
}

func TestSigningRejectsPrehashedInputAndUninitializedSigners(t *testing.T) {
	t.Parallel()

	signer := deterministicManagementSigner(t)
	if _, err := signer.Sign(nil, []byte("digest"), crypto.SHA256); !errors.Is(
		err,
		ErrUnsupportedHash,
	) {
		t.Fatalf("Sign(prehashed) error = %v, want ErrUnsupportedHash", err)
	}
	if _, err := signer.Sign(
		nil,
		[]byte("message"),
		&ed25519.Options{Context: "unexpected-context"},
	); !errors.Is(err, ErrUnsupportedOptions) {
		t.Fatalf("Sign(context) error = %v, want ErrUnsupportedOptions", err)
	}
	var empty ManagementSigner
	if _, err := empty.Sign(nil, []byte("message"), crypto.Hash(0)); !errors.Is(
		err,
		ErrSignerNotReady,
	) {
		t.Fatalf("empty Sign() error = %v, want ErrSignerNotReady", err)
	}
}

func TestSessionSignerIsMemoryOnlyAndSigns(t *testing.T) {
	t.Parallel()

	generated, err := generateSigner(bytes.NewReader(bytes.Repeat([]byte{0x5A}, ed25519.SeedSize)))
	if err != nil {
		t.Fatalf("generateSigner() error = %v", err)
	}
	session := &SessionSigner{signer: generated}
	message := []byte("ephemeral session assertion")
	signature, err := session.Sign(nil, message, crypto.Hash(0))
	if err != nil {
		t.Fatalf("Sign() error = %v", err)
	}
	public := session.Public().(ed25519.PublicKey)
	if !ed25519.Verify(public, message, signature) {
		t.Fatal("session signature did not verify")
	}

	if _, supported := any(session).(encoding.BinaryMarshaler); supported {
		t.Fatal("session signer unexpectedly implements BinaryMarshaler")
	}
	if _, supported := any(session).(interface{ Save(keystore.Store) error }); supported {
		t.Fatal("session signer unexpectedly exposes persistence")
	}
}

func TestSignerAndPersistenceErrorsDoNotRenderPrivateKeys(t *testing.T) {
	t.Parallel()

	signer := deterministicManagementSigner(t)
	generatedSession, err := generateSigner(
		bytes.NewReader(bytes.Repeat([]byte{0x4C}, ed25519.SeedSize)),
	)
	if err != nil {
		t.Fatalf("generateSigner(session) error = %v", err)
	}
	session := &SessionSigner{signer: generatedSession}
	private := signer.signer.private
	privateEncodings := []string{
		hex.EncodeToString(private),
		base64.StdEncoding.EncodeToString(private),
		fmt.Sprint([]byte(private)),
		hex.EncodeToString(session.signer.private),
		base64.StdEncoding.EncodeToString(session.signer.private),
		fmt.Sprint([]byte(session.signer.private)),
	}
	rendered := []string{
		fmt.Sprintf("%v", signer),
		fmt.Sprintf("%+v", signer),
		fmt.Sprintf("%#v", signer),
		fmt.Sprintf("%s", signer),
		fmt.Sprintf("%v", *signer),
		fmt.Sprintf("%+v", *signer),
		fmt.Sprintf("%#v", *signer),
		fmt.Sprintf("%s", *signer),
		fmt.Sprintf("%v", session),
		fmt.Sprintf("%+v", session),
		fmt.Sprintf("%#v", session),
		fmt.Sprintf("%s", session),
		fmt.Sprintf("%v", *session),
		fmt.Sprintf("%+v", *session),
		fmt.Sprintf("%#v", *session),
		fmt.Sprintf("%s", *session),
	}
	for _, output := range rendered {
		for _, secret := range privateEncodings {
			if strings.Contains(output, secret) {
				t.Fatalf("formatted signer exposed private key: %q", output)
			}
		}
		if !strings.Contains(output, "redacted") {
			t.Fatalf("formatted signer lacks redaction marker: %q", output)
		}
	}

	privateError := verbosePrivateError{Secret: "private-key=" + privateEncodings[0]}
	store := &memoryStore{saveError: privateError}
	err = SaveManagementSigner(store, signer)
	if !errors.Is(err, ErrPersistence) || !errors.Is(err, privateError) {
		t.Fatalf("SaveManagementSigner() error lost classification or cause: %v", err)
	}
	typed := err.(*persistenceError)
	forms := []any{
		typed,
		*typed,
		fmt.Errorf("wrapped pointer: %w", typed),
		fmt.Errorf("wrapped value: %w", *typed),
	}
	for _, form := range forms {
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			rendered := fmt.Sprintf(format, form)
			if strings.Contains(rendered, privateError.Secret) {
				t.Fatalf("%s exposed private cause from %T: %q", format, form, rendered)
			}
		}
	}
	code, message, exitCode := diagnostic.Public(err)
	if code != "unexpected_error" || message != "an unexpected error occurred" ||
		exitCode != diagnostic.ExitFailure {
		t.Fatalf(
			"Public() = (%q, %q, %d), want generic failure",
			code,
			message,
			exitCode,
		)
	}
	if strings.Contains(code+message, privateEncodings[0]) {
		t.Fatalf("public diagnostic exposed private cause: %q %q", code, message)
	}
}

func TestPublicReturnsDetachedBytes(t *testing.T) {
	t.Parallel()

	signer := deterministicManagementSigner(t)
	first := signer.Public().(ed25519.PublicKey)
	first[0] ^= 0xFF
	second := signer.Public().(ed25519.PublicKey)
	if bytes.Equal(first, second) {
		t.Fatal("Public() returned signer-owned bytes")
	}
}
