package agentauth

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"testing"
)

var enrollmentPayloadFields = []string{
	"client_nonce",
	"create_if_missing",
	"exp",
	"iat",
	"issuer",
	"jti",
	"operation",
	"version",
}

var protectedHeaderFields = []string{"typ", "alg", "jwk", "nonce", "url"}

type signedEnrollmentFixture struct {
	ProtocolVersion         string            `json:"protocol_version"`
	Issuer                  string            `json:"issuer"`
	VerificationTime        int64             `json:"verification_time"`
	ManagementJWK           PublicJWK         `json:"management_jwk"`
	ManagementJWKThumbprint string            `json:"management_jwk_thumbprint"`
	Request                 flattenedJWS      `json:"request"`
	Expected                enrollmentFixture `json:"expected"`
}

type enrollmentFixture struct {
	Nonce   string            `json:"nonce"`
	Payload enrollmentPayload `json:"payload"`
}

func TestPinnedPhase02BWireProfileAndSignedEnrollmentFixture(t *testing.T) {
	t.Parallel()

	wire, err := os.ReadFile("testdata/wire-profile.v0.1.json")
	if err != nil {
		t.Fatalf("ReadFile(wire profile) error = %v", err)
	}
	var profile map[string]json.RawMessage
	if err := decodeExactJSONObject(
		wire,
		&profile,
		[]string{"protocol_version", "discovery_fixture", "account_resolution"},
	); err != nil {
		t.Fatalf("decodeExactJSONObject(wire profile) error = %v", err)
	}
	var profileVersion string
	if err := json.Unmarshal(profile["protocol_version"], &profileVersion); err != nil {
		t.Fatalf("Unmarshal(protocol_version) error = %v", err)
	}
	var account map[string]json.RawMessage
	if err := json.Unmarshal(profile["account_resolution"], &account); err != nil {
		t.Fatalf("Unmarshal(account_resolution) error = %v", err)
	}
	var endpoint string
	if err := json.Unmarshal(account["endpoint_path"], &endpoint); err != nil {
		t.Fatalf("Unmarshal(endpoint_path) error = %v", err)
	}
	if profileVersion != ProtocolVersion || endpoint != "/api/auth/agent/enroll" {
		t.Fatalf("unexpected wire profile version=%q endpoint=%q", profileVersion, endpoint)
	}

	encoded, err := os.ReadFile("testdata/signed-enrollment.v0.1.json")
	if err != nil {
		t.Fatalf("ReadFile(signed enrollment) error = %v", err)
	}
	var fixture signedEnrollmentFixture
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatalf("Unmarshal(signed enrollment) error = %v", err)
	}
	if fixture.ProtocolVersion != ProtocolVersion {
		t.Fatalf("fixture protocol version = %q", fixture.ProtocolVersion)
	}
	payload, err := rawBase64URL.DecodeString(fixture.Request.Payload)
	if err != nil {
		t.Fatalf("DecodeString(payload) error = %v", err)
	}
	jwk, err := verifyFlattenedJWS(
		fixture.Request,
		enrollmentProofType,
		fixture.Issuer+"/api/auth/agent/enroll",
		payload,
	)
	if err != nil {
		t.Fatalf("verifyFlattenedJWS(shared fixture) error = %v", err)
	}
	if jwk != fixture.ManagementJWK {
		t.Fatalf("verified JWK = %#v, want %#v", jwk, fixture.ManagementJWK)
	}
	thumbprint, err := jwkThumbprint(jwk)
	if err != nil {
		t.Fatalf("jwkThumbprint() error = %v", err)
	}
	if thumbprint != fixture.ManagementJWKThumbprint {
		t.Fatalf("thumbprint = %q", thumbprint)
	}
	var decodedPayload enrollmentPayload
	if err := decodeExactJSONObject(payload, &decodedPayload, enrollmentPayloadFields); err != nil {
		t.Fatalf("decodeExactJSONObject(payload) error = %v", err)
	}
	if decodedPayload != fixture.Expected.Payload {
		t.Fatalf("payload = %#v, want %#v", decodedPayload, fixture.Expected.Payload)
	}
	protected, err := rawBase64URL.DecodeString(fixture.Request.Protected)
	if err != nil {
		t.Fatalf("DecodeString(protected) error = %v", err)
	}
	var header protectedHeader
	if err := decodeExactJSONObject(protected, &header, protectedHeaderFields); err != nil {
		t.Fatalf("decodeExactJSONObject(protected) error = %v", err)
	}
	if header.Nonce != fixture.Expected.Nonce {
		t.Fatalf("nonce = %q, want %q", header.Nonce, fixture.Expected.Nonce)
	}
	generatedPayload, err := json.Marshal(fixture.Expected.Payload)
	if err != nil {
		t.Fatalf("Marshal(expected payload) error = %v", err)
	}
	generated, err := signFlattenedJWS(
		bytes.NewReader(nil),
		newFixtureSigner(t),
		generatedPayload,
		protectedHeader{
			Type:  enrollmentProofType,
			Alg:   "EdDSA",
			JWK:   &fixture.ManagementJWK,
			Nonce: fixture.Expected.Nonce,
			URL:   fixture.Issuer + "/api/auth/agent/enroll",
		},
	)
	if err != nil {
		t.Fatalf("signFlattenedJWS(shared fixture) error = %v", err)
	}
	if generated != fixture.Request {
		t.Fatalf("Go-generated JWS differs from shared fixture: %#v", generated)
	}
}

func TestPhase02BValidatorAcceptsSharedFixture(t *testing.T) {
	protocolPath := os.Getenv("KADO_PHASE_02B_PROTOCOL")
	if protocolPath == "" {
		t.Skip("set KADO_PHASE_02B_PROTOCOL to run the authoritative TypeScript validator")
	}
	runtime := os.Getenv("KADO_TYPESCRIPT_RUNTIME")
	if runtime == "" {
		runtime = "bun"
	}
	command := exec.CommandContext(
		context.Background(),
		runtime,
		"testdata/verify-phase02b-fixture.ts",
		"testdata/signed-enrollment.v0.1.json",
		protocolPath,
	)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf("Phase 02B fixture verification error = %v; output = %s", err, output.String())
	}
}
