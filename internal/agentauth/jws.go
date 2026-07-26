package agentauth

import (
	"bytes"
	"crypto"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
)

var rawBase64URL = base64.RawURLEncoding.Strict()

type PublicJWK struct {
	Kty string `json:"kty"`
	Crv string `json:"crv"`
	X   string `json:"x"`
	Alg string `json:"alg,omitempty"`
}

type flattenedJWS struct {
	Protected string `json:"protected"`
	Payload   string `json:"payload"`
	Signature string `json:"signature"`
}

type protectedHeader struct {
	Type  string     `json:"typ"`
	Alg   string     `json:"alg"`
	JWK   *PublicJWK `json:"jwk,omitempty"`
	Nonce string     `json:"nonce,omitempty"`
	URL   string     `json:"url"`
}

func publicJWK(signer crypto.Signer) (PublicJWK, error) {
	if signer == nil {
		return PublicJWK{}, newProtocolError(ErrAuthentication, nil)
	}
	public, ok := signer.Public().(ed25519.PublicKey)
	if !ok || len(public) != ed25519.PublicKeySize {
		return PublicJWK{}, newProtocolError(ErrAuthentication, nil)
	}
	return PublicJWK{
		Kty: "OKP",
		Crv: "Ed25519",
		X:   rawBase64URL.EncodeToString(public),
		Alg: "EdDSA",
	}, nil
}

func validatePublicJWK(jwk PublicJWK) (ed25519.PublicKey, error) {
	if jwk.Kty != "OKP" || jwk.Crv != "Ed25519" || jwk.Alg != "EdDSA" {
		return nil, newProtocolError(ErrProtocol, nil)
	}
	public, err := rawBase64URL.DecodeString(jwk.X)
	if err != nil || len(public) != ed25519.PublicKeySize {
		return nil, newProtocolError(ErrProtocol, err)
	}
	return ed25519.PublicKey(append([]byte(nil), public...)), nil
}

func jwkThumbprint(jwk PublicJWK) (string, error) {
	if _, err := validatePublicJWK(jwk); err != nil {
		return "", err
	}
	canonical, err := json.Marshal(struct {
		Crv string `json:"crv"`
		Kty string `json:"kty"`
		X   string `json:"x"`
	}{
		Crv: jwk.Crv,
		Kty: jwk.Kty,
		X:   jwk.X,
	})
	if err != nil {
		return "", newProtocolError(ErrProtocol, err)
	}
	digest := sha256.Sum256(canonical)
	return rawBase64URL.EncodeToString(digest[:]), nil
}

func signFlattenedJWS(
	random io.Reader,
	signer crypto.Signer,
	payload []byte,
	header protectedHeader,
) (flattenedJWS, error) {
	if header.Type == "" || header.Alg != "EdDSA" || header.URL == "" {
		return flattenedJWS{}, newProtocolError(ErrProtocol, nil)
	}
	protected, err := json.Marshal(header)
	if err != nil {
		return flattenedJWS{}, newProtocolError(ErrProtocol, err)
	}
	encodedProtected := rawBase64URL.EncodeToString(protected)
	encodedPayload := rawBase64URL.EncodeToString(payload)
	signingInput := encodedProtected + "." + encodedPayload
	signature, err := signer.Sign(random, []byte(signingInput), crypto.Hash(0))
	if err != nil {
		return flattenedJWS{}, newProtocolError(ErrAuthentication, err)
	}
	if len(signature) != ed25519.SignatureSize {
		return flattenedJWS{}, newProtocolError(ErrAuthentication, nil)
	}
	return flattenedJWS{
		Protected: encodedProtected,
		Payload:   encodedPayload,
		Signature: rawBase64URL.EncodeToString(signature),
	}, nil
}

func verifyFlattenedJWS(
	jws flattenedJWS,
	expectedType string,
	expectedURL string,
	expectedPayload []byte,
) (PublicJWK, error) {
	protectedBytes, err := rawBase64URL.DecodeString(jws.Protected)
	if err != nil {
		return PublicJWK{}, newProtocolError(ErrProtocol, err)
	}
	var header protectedHeader
	if err := decodeStrictJSON(protectedBytes, &header, true); err != nil {
		return PublicJWK{}, newProtocolError(ErrProtocol, err)
	}
	if header.Type != expectedType ||
		header.Alg != "EdDSA" ||
		header.URL != expectedURL ||
		header.JWK == nil {
		return PublicJWK{}, newProtocolError(ErrProtocol, nil)
	}
	public, err := validatePublicJWK(*header.JWK)
	if err != nil {
		return PublicJWK{}, err
	}
	payload, err := rawBase64URL.DecodeString(jws.Payload)
	if err != nil || !bytes.Equal(payload, expectedPayload) {
		return PublicJWK{}, newProtocolError(ErrProtocol, err)
	}
	signature, err := rawBase64URL.DecodeString(jws.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize {
		return PublicJWK{}, newProtocolError(ErrProtocol, err)
	}
	if !ed25519.Verify(public, []byte(jws.Protected+"."+jws.Payload), signature) {
		return PublicJWK{}, newProtocolError(ErrProtocol, errors.New("signature mismatch"))
	}
	return *header.JWK, nil
}

func decodeBase64URL(value string, minimum, maximum int) ([]byte, error) {
	if value == "" || len(value) > maximum*2 {
		return nil, newProtocolError(ErrProtocol, nil)
	}
	decoded, err := rawBase64URL.DecodeString(value)
	if err != nil || len(decoded) < minimum || len(decoded) > maximum {
		return nil, newProtocolError(ErrProtocol, err)
	}
	return decoded, nil
}
