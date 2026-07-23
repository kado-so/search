package agentauth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
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

func TestPhase02BValidatorAcceptsGoAdmissionSolutionAndProofs(t *testing.T) {
	admissionPath := os.Getenv("KADO_PHASE_02B_ADMISSION")
	if admissionPath == "" {
		t.Skip("set KADO_PHASE_02B_ADMISSION to run the authoritative TypeScript validator")
	}
	profile := loadAdmissionFixture(t)
	bindingInput := fixtureBindingInput(profile)
	binding, err := encodeAdmissionBinding(bindingInput)
	if err != nil {
		t.Fatalf("encodeAdmissionBinding() error = %v", err)
	}
	management := newFixtureSigner(t)
	session := fixtureSigner{
		private: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x24}, ed25519.SeedSize)),
	}
	managementJWK, err := publicJWK(management)
	if err != nil {
		t.Fatalf("publicJWK(management) error = %v", err)
	}
	sessionJWK, err := publicJWK(session)
	if err != nil {
		t.Fatalf("publicJWK(session) error = %v", err)
	}
	endpoint := bindingInput.Endpoint
	managementProof, err := signFlattenedJWS(
		bytes.NewReader(nil),
		management,
		binding,
		protectedHeader{
			Type: admissionManagementProofType,
			Alg:  "EdDSA",
			JWK:  &managementJWK,
			URL:  endpoint,
		},
	)
	if err != nil {
		t.Fatalf("sign management proof error = %v", err)
	}
	sessionProof, err := signFlattenedJWS(
		bytes.NewReader(nil),
		session,
		binding,
		protectedHeader{
			Type: admissionSessionProofType,
			Alg:  "EdDSA",
			JWK:  &sessionJWK,
			URL:  endpoint,
		},
	)
	if err != nil {
		t.Fatalf("sign session proof error = %v", err)
	}
	fixture := struct {
		BindingInput    admissionInteropBinding `json:"binding_input"`
		Binding         string                  `json:"binding_statement"`
		Challenge       admissionChallenge      `json:"challenge"`
		Solution        admissionSolution       `json:"solution"`
		HMACSecret      string                  `json:"hmac_secret"`
		ManagementJWK   PublicJWK               `json:"management_jwk"`
		ManagementProof flattenedJWS            `json:"management_proof"`
		SessionJWK      PublicJWK               `json:"session_jwk"`
		SessionProof    flattenedJWS            `json:"session_proof"`
		Endpoint        string                  `json:"endpoint"`
	}{
		BindingInput: interopBinding(bindingInput),
		Binding:      rawBase64URL.EncodeToString(binding),
		Challenge:    fixtureChallenge(profile),
		Solution: admissionSolution{
			Counter:    profile.Vector.Solution.Counter,
			DerivedKey: profile.Vector.Solution.DerivedKey,
		},
		HMACSecret:      profile.Vector.HMACSecretBase64URL,
		ManagementJWK:   managementJWK,
		ManagementProof: managementProof,
		SessionJWK:      sessionJWK,
		SessionProof:    sessionProof,
		Endpoint:        endpoint,
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("Marshal(admission interop fixture) error = %v", err)
	}
	fixturePath := filepath.Join(t.TempDir(), "go-admission.json")
	if err := os.WriteFile(fixturePath, encoded, 0o600); err != nil {
		t.Fatalf("WriteFile(admission interop fixture) error = %v", err)
	}
	runtime := os.Getenv("KADO_TYPESCRIPT_RUNTIME")
	if runtime == "" {
		runtime = "node"
	}
	validatorPath := admissionPath
	runtimeArguments := []string{}
	if filepath.Base(runtime) == "node" {
		validatorPath = prepareNodeAdmissionValidator(t, admissionPath)
		runtimeArguments = append(runtimeArguments, "--experimental-strip-types")
	}
	runtimeArguments = append(
		runtimeArguments,
		"testdata/verify-phase02b-admission.ts",
		fixturePath,
		validatorPath,
	)
	command := exec.CommandContext(
		context.Background(),
		runtime,
		runtimeArguments...,
	)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf(
			"Phase 02B admission verification error = %v; output = %s",
			err,
			output.String(),
		)
	}
}

func TestPhase02BValidatorAcceptsGoPrivateKeyJWT(t *testing.T) {
	tokenPath := os.Getenv("KADO_PHASE_02B_TOKEN")
	if tokenPath == "" {
		t.Skip("set KADO_PHASE_02B_TOKEN to run the authoritative TypeScript validator")
	}
	session := fixtureSigner{
		private: ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x35}, ed25519.SeedSize)),
	}
	sessionJWK, err := publicJWK(session)
	if err != nil {
		t.Fatalf("publicJWK(session) error = %v", err)
	}
	const (
		clientID = "clt_00000000000000000000000000000001"
		keyID    = "scred_00000000000000000000000000000001"
		issuer   = "https://kado.so"
		now      = int64(1784370000)
	)
	assertion, err := signCompactJWT(
		bytes.NewReader(nil),
		session,
		privateKeyJWTHeader{
			Algorithm: "EdDSA",
			KeyID:     keyID,
			Type:      "JWT",
		},
		privateKeyJWTClaims{
			Audience:  issuer + "/oauth/token",
			ExpiresAt: now + 60,
			IssuedAt:  now,
			Issuer:    clientID,
			JTI:       rawBase64URL.EncodeToString(bytes.Repeat([]byte{0x45}, 16)),
			Subject:   clientID,
		},
	)
	if err != nil {
		t.Fatalf("signCompactJWT() error = %v", err)
	}
	fixture := struct {
		Assertion  string    `json:"assertion"`
		PublicJWK  PublicJWK `json:"public_jwk"`
		ClientID   string    `json:"client_id"`
		KeyID      string    `json:"key_id"`
		Issuer     string    `json:"issuer"`
		NowSeconds int64     `json:"now_seconds"`
	}{
		Assertion:  assertion,
		PublicJWK:  sessionJWK,
		ClientID:   clientID,
		KeyID:      keyID,
		Issuer:     issuer,
		NowSeconds: now,
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("Marshal(private_key_jwt fixture) error = %v", err)
	}
	fixturePath := filepath.Join(t.TempDir(), "go-private-key-jwt.json")
	if err := os.WriteFile(fixturePath, encoded, 0o600); err != nil {
		t.Fatalf("WriteFile(private_key_jwt fixture) error = %v", err)
	}
	runtime := os.Getenv("KADO_TYPESCRIPT_RUNTIME")
	if runtime == "" {
		runtime = "node"
	}
	validatorPath := tokenPath
	runtimeArguments := []string{}
	if filepath.Base(runtime) == "node" {
		validatorPath = prepareNodeTokenValidator(t, tokenPath)
		runtimeArguments = append(runtimeArguments, "--experimental-strip-types")
	}
	runtimeArguments = append(
		runtimeArguments,
		"testdata/verify-phase02b-token.ts",
		fixturePath,
		validatorPath,
	)
	command := exec.CommandContext(context.Background(), runtime, runtimeArguments...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf(
			"Phase 02B private_key_jwt verification error = %v; output = %s",
			err,
			output.String(),
		)
	}
}

func TestPhase02BValidatorAcceptsGoCredentialProof(t *testing.T) {
	protocolPath := os.Getenv("KADO_PHASE_02B_PROTOCOL")
	if protocolPath == "" {
		t.Skip("set KADO_PHASE_02B_PROTOCOL to run the authoritative TypeScript validator")
	}
	management := newFixtureSigner(t)
	managementJWK, err := publicJWK(management)
	if err != nil {
		t.Fatalf("publicJWK(management) error = %v", err)
	}
	thumbprint, err := jwkThumbprint(managementJWK)
	if err != nil {
		t.Fatalf("jwkThumbprint(management) error = %v", err)
	}
	const (
		issuer = "https://kado.so"
		now    = int64(1784370000)
	)
	nonce := rawBase64URL.EncodeToString(bytes.Repeat([]byte{0x56}, 24))
	payload := credentialPayload{
		ExpiresAt: now + 60,
		IssuedAt:  now,
		Issuer:    issuer,
		JTI:       rawBase64URL.EncodeToString(bytes.Repeat([]byte{0x67}, 16)),
		Operation: credentialRevokeOperation,
		Version:   ProtocolVersion,
	}
	payloadBytes, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("Marshal(credential payload) error = %v", err)
	}
	proof, err := signFlattenedJWS(
		bytes.NewReader(nil),
		management,
		payloadBytes,
		protectedHeader{
			Type:  credentialProofType,
			Alg:   "EdDSA",
			JWK:   &managementJWK,
			Nonce: nonce,
			URL:   issuer + "/api/auth/agent/credentials",
		},
	)
	if err != nil {
		t.Fatalf("signFlattenedJWS(credential) error = %v", err)
	}
	fixture := struct {
		Issuer     string            `json:"issuer"`
		NowSeconds int64             `json:"now_seconds"`
		Nonce      string            `json:"nonce"`
		Thumbprint string            `json:"thumbprint"`
		PublicJWK  PublicJWK         `json:"public_jwk"`
		Payload    credentialPayload `json:"payload"`
		Request    flattenedJWS      `json:"request"`
	}{
		Issuer:     issuer,
		NowSeconds: now,
		Nonce:      nonce,
		Thumbprint: thumbprint,
		PublicJWK:  managementJWK,
		Payload:    payload,
		Request:    proof,
	}
	encoded, err := json.Marshal(fixture)
	if err != nil {
		t.Fatalf("Marshal(credential fixture) error = %v", err)
	}
	fixturePath := filepath.Join(t.TempDir(), "go-credential-proof.json")
	if err := os.WriteFile(fixturePath, encoded, 0o600); err != nil {
		t.Fatalf("WriteFile(credential fixture) error = %v", err)
	}
	runtime := os.Getenv("KADO_TYPESCRIPT_RUNTIME")
	if runtime == "" {
		runtime = "node"
	}
	validatorPath := protocolPath
	runtimeArguments := []string{}
	if filepath.Base(runtime) == "node" {
		validatorPath = prepareNodeProtocolValidator(t, protocolPath)
		runtimeArguments = append(runtimeArguments, "--experimental-strip-types")
	}
	runtimeArguments = append(
		runtimeArguments,
		"testdata/verify-phase02b-credential.ts",
		fixturePath,
		validatorPath,
	)
	command := exec.CommandContext(context.Background(), runtime, runtimeArguments...)
	var output bytes.Buffer
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Run(); err != nil {
		t.Fatalf(
			"Phase 02B credential proof verification error = %v; output = %s",
			err,
			output.String(),
		)
	}
}

func prepareNodeAdmissionValidator(t *testing.T, admissionPath string) string {
	t.Helper()
	admissionSource, err := os.ReadFile(admissionPath)
	if err != nil {
		t.Fatalf("ReadFile(Phase 02B admission validator) error = %v", err)
	}
	protocolPath := filepath.Join(filepath.Dir(admissionPath), "protocol.ts")
	protocolSource, err := os.ReadFile(protocolPath)
	if err != nil {
		t.Fatalf("ReadFile(Phase 02B protocol dependency) error = %v", err)
	}
	directory := t.TempDir()
	copiedAdmission := filepath.Join(directory, "admission.ts")
	copiedProtocol := filepath.Join(directory, "protocol.ts")
	adaptedAdmission := bytes.ReplaceAll(
		admissionSource,
		[]byte(`"./protocol"`),
		[]byte(`"./protocol.ts"`),
	)
	adaptedProtocol := adaptProtocolForNode(protocolSource)
	if err := os.WriteFile(copiedAdmission, adaptedAdmission, 0o600); err != nil {
		t.Fatalf("WriteFile(admission validator copy) error = %v", err)
	}
	if err := os.WriteFile(copiedProtocol, adaptedProtocol, 0o600); err != nil {
		t.Fatalf("WriteFile(protocol validator copy) error = %v", err)
	}
	return copiedAdmission
}

func prepareNodeProtocolValidator(t *testing.T, protocolPath string) string {
	t.Helper()
	protocolSource, err := os.ReadFile(protocolPath)
	if err != nil {
		t.Fatalf("ReadFile(Phase 02B protocol validator) error = %v", err)
	}
	copiedProtocol := filepath.Join(t.TempDir(), "protocol.ts")
	if err := os.WriteFile(
		copiedProtocol,
		adaptProtocolForNode(protocolSource),
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(protocol validator copy) error = %v", err)
	}
	return copiedProtocol
}

func prepareNodeTokenValidator(t *testing.T, tokenPath string) string {
	t.Helper()
	tokenSource, err := os.ReadFile(tokenPath)
	if err != nil {
		t.Fatalf("ReadFile(Phase 02B token validator) error = %v", err)
	}
	protocolPath := filepath.Join(filepath.Dir(tokenPath), "protocol.ts")
	protocolSource, err := os.ReadFile(protocolPath)
	if err != nil {
		t.Fatalf("ReadFile(Phase 02B protocol dependency) error = %v", err)
	}
	directory := t.TempDir()
	copiedToken := filepath.Join(directory, "token.ts")
	copiedProtocol := filepath.Join(directory, "protocol.ts")
	adaptedToken := bytes.ReplaceAll(
		tokenSource,
		[]byte(`"./protocol"`),
		[]byte(`"./protocol.ts"`),
	)
	if err := os.WriteFile(copiedToken, adaptedToken, 0o600); err != nil {
		t.Fatalf("WriteFile(token validator copy) error = %v", err)
	}
	if err := os.WriteFile(copiedProtocol, adaptProtocolForNode(protocolSource), 0o600); err != nil {
		t.Fatalf("WriteFile(protocol validator copy) error = %v", err)
	}
	return copiedToken
}

func adaptProtocolForNode(protocolSource []byte) []byte {
	adaptedProtocol := bytes.ReplaceAll(
		protocolSource,
		[]byte("export class AgentAuthProtocolError extends Error {\n  readonly status: number;"),
		[]byte("export class AgentAuthProtocolError extends Error {\n  readonly code: AgentAuthErrorCode;\n  readonly status: number;"),
	)
	adaptedProtocol = bytes.ReplaceAll(
		adaptedProtocol,
		[]byte("  constructor(readonly code: AgentAuthErrorCode) {\n    const definition = errorDefinitions[code];\n    super(definition.message);"),
		[]byte("  constructor(code: AgentAuthErrorCode) {\n    const definition = errorDefinitions[code];\n    super(definition.message);\n    this.code = code;"),
	)
	adaptedProtocol = bytes.ReplaceAll(
		adaptedProtocol,
		[]byte("class JsonScanner {\n  private offset = 0;\n\n  constructor(private readonly text: string) {}"),
		[]byte("class JsonScanner {\n  private offset = 0;\n  private readonly text: string;\n\n  constructor(text: string) { this.text = text; }"),
	)
	return adaptedProtocol
}

type admissionInteropBinding struct {
	ProtocolVersion      string   `json:"protocolVersion"`
	Operation            string   `json:"operation"`
	Issuer               string   `json:"issuer"`
	Endpoint             string   `json:"endpoint"`
	TransactionID        string   `json:"transactionId"`
	ExpiresAt            int64    `json:"expiresAt"`
	ManagementThumbprint string   `json:"managementThumbprint"`
	SessionThumbprint    string   `json:"sessionThumbprint"`
	RequestedScopes      []string `json:"requestedScopes"`
	Audience             string   `json:"audience"`
	ServerNonce          string   `json:"serverNonce"`
	ClientNonce          string   `json:"clientNonce"`
	CreateIfMissing      bool     `json:"createIfMissing"`
}

func interopBinding(input admissionBindingInput) admissionInteropBinding {
	return admissionInteropBinding{
		ProtocolVersion:      input.ProtocolVersion,
		Operation:            input.Operation,
		Issuer:               input.Issuer,
		Endpoint:             input.Endpoint,
		TransactionID:        input.TransactionID,
		ExpiresAt:            input.ExpiresAt,
		ManagementThumbprint: input.ManagementThumbprint,
		SessionThumbprint:    input.SessionThumbprint,
		RequestedScopes:      input.RequestedScopes,
		Audience:             input.Audience,
		ServerNonce:          input.ServerNonce,
		ClientNonce:          input.ClientNonce,
		CreateIfMissing:      input.CreateIfMissing,
	}
}
