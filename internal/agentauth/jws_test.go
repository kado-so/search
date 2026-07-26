package agentauth

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

type privateCause struct {
	Secret string
}

func (cause privateCause) Error() string {
	return "private protocol failure"
}

type fixtureSigner struct {
	private ed25519.PrivateKey
}

func (signer fixtureSigner) Public() crypto.PublicKey {
	return append(ed25519.PublicKey(nil), signer.private.Public().(ed25519.PublicKey)...)
}

func (signer fixtureSigner) Sign(
	_ io.Reader,
	message []byte,
	options crypto.SignerOpts,
) ([]byte, error) {
	if options.HashFunc() != crypto.Hash(0) {
		return nil, ErrAuthentication
	}
	return ed25519.Sign(signer.private, message), nil
}

func newFixtureSigner(t *testing.T) fixtureSigner {
	t.Helper()
	seed, err := hex.DecodeString(
		"9d61b19deffd5a60ba844af492ec2cc4" +
			"4449c5697b326919703bac031cae7f60",
	)
	if err != nil {
		t.Fatalf("DecodeString(seed) error = %v", err)
	}
	return fixtureSigner{private: ed25519.NewKeyFromSeed(seed)}
}

func TestRFC8032PublicJWKAndRFC7638ThumbprintFixture(t *testing.T) {
	t.Parallel()

	jwk, err := publicJWK(newFixtureSigner(t))
	if err != nil {
		t.Fatalf("publicJWK() error = %v", err)
	}
	if jwk != (PublicJWK{
		Kty: "OKP",
		Crv: "Ed25519",
		X:   "11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo",
		Alg: "EdDSA",
	}) {
		t.Fatalf("publicJWK() = %#v", jwk)
	}
	thumbprint, err := jwkThumbprint(jwk)
	if err != nil {
		t.Fatalf("jwkThumbprint() error = %v", err)
	}
	if thumbprint != "kPrK_qmxVWaYVA9wwBF6Iuo3vVzz7TxHCTwXBygrS4k" {
		t.Fatalf("thumbprint = %q", thumbprint)
	}
}

func TestFlattenedJWSSignsExactBytesAndRejectsTamper(t *testing.T) {
	t.Parallel()

	signer := newFixtureSigner(t)
	jwk, err := publicJWK(signer)
	if err != nil {
		t.Fatalf("publicJWK() error = %v", err)
	}
	payload := []byte(`{"fixture":"agent-auth-go"}`)
	jws, err := signFlattenedJWS(bytes.NewReader(nil), signer, payload, protectedHeader{
		Type:  enrollmentProofType,
		Alg:   "EdDSA",
		JWK:   &jwk,
		Nonce: "bm9uY2UtZml4dHVyZS0xMjM0NQ",
		URL:   "https://kado.so/api/auth/agent/enroll",
	})
	if err != nil {
		t.Fatalf("signFlattenedJWS() error = %v", err)
	}
	if jws.Protected != "eyJ0eXAiOiJhZ2VudC1lbnJvbGxtZW50K2p3cyIsImFsZyI6IkVkRFNBIiwiandrIjp7Imt0eSI6Ik9LUCIsImNydiI6IkVkMjU1MTkiLCJ4IjoiMTFxWUFZS3hDcmZWU183VHlXUUhPZzdoY3ZQYXBpTWxyd0lhYVBjSFVSbyIsImFsZyI6IkVkRFNBIn0sIm5vbmNlIjoiYm05dVkyVXRabWw0ZEhWeVpTMHhNak0wTlEiLCJ1cmwiOiJodHRwczovL2thZG8uc28vYXBpL2F1dGgvYWdlbnQvZW5yb2xsIn0" {
		t.Fatalf("protected fixture changed: %q", jws.Protected)
	}
	if jws.Payload != "eyJmaXh0dXJlIjoiYWdlbnQtYXV0aC1nbyJ9" {
		t.Fatalf("payload fixture changed: %q", jws.Payload)
	}
	if _, err := verifyFlattenedJWS(
		jws,
		enrollmentProofType,
		"https://kado.so/api/auth/agent/enroll",
		payload,
	); err != nil {
		t.Fatalf("verifyFlattenedJWS() error = %v", err)
	}

	for name, mutate := range map[string]func(*flattenedJWS){
		"protected": func(value *flattenedJWS) { value.Protected = flipBase64(value.Protected) },
		"payload":   func(value *flattenedJWS) { value.Payload = flipBase64(value.Payload) },
		"signature": func(value *flattenedJWS) { value.Signature = flipBase64(value.Signature) },
	} {
		t.Run(name, func(t *testing.T) {
			tampered := jws
			mutate(&tampered)
			if _, err := verifyFlattenedJWS(
				tampered,
				enrollmentProofType,
				"https://kado.so/api/auth/agent/enroll",
				payload,
			); err == nil {
				t.Fatal("verifyFlattenedJWS(tampered) succeeded")
			}
		})
	}
}

func TestStrictJSONRejectsDuplicateMembersAtEveryDepth(t *testing.T) {
	t.Parallel()

	for _, encoded := range []string{
		`{"a":1,"a":2}`,
		`{"outer":{"a":1,"a":2}}`,
		`[{"a":1,"a":2}]`,
	} {
		var destination any
		if err := decodeStrictJSON([]byte(encoded), &destination, false); err == nil {
			t.Fatalf("decodeStrictJSON(%q) succeeded", encoded)
		}
	}
}

func TestProtocolErrorsDoNotReflectPrivateCauses(t *testing.T) {
	t.Parallel()

	cause := privateCause{Secret: "private-protocol-marker"}
	errorValue := newProtocolError(ErrProtocol, cause).(*protocolError)
	if !errors.Is(errorValue, ErrProtocol) || !errors.Is(errorValue, cause) {
		t.Fatal("protocol error lost public classification or trusted cause")
	}
	forms := []any{
		errorValue,
		*errorValue,
		fmt.Errorf("wrapped pointer: %w", errorValue),
		fmt.Errorf("wrapped value: %w", *errorValue),
	}
	for _, form := range forms {
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			rendered := fmt.Sprintf(format, form)
			if strings.Contains(rendered, cause.Secret) {
				t.Fatalf("%s exposed private cause from %T: %q", format, form, rendered)
			}
		}
	}
}

func flipBase64(value string) string {
	if value == "" {
		return "A"
	}
	replacement := byte('A')
	if value[0] == replacement {
		replacement = 'B'
	}
	return string(replacement) + value[1:]
}
