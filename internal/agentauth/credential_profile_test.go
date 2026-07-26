package agentauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestPinnedCredentialProfile(t *testing.T) {
	t.Parallel()

	encoded, err := os.ReadFile("testdata/credential-profile.v0.1.json")
	if err != nil {
		t.Fatalf("ReadFile(credential profile) error = %v", err)
	}
	digest := sha256.Sum256(encoded)
	if got := hex.EncodeToString(digest[:]); got !=
		"65cb8d3adaeac1d48b89c82bb4497435993382bd4c9a9df64b3ed17e9b23dcbf" {
		t.Fatalf("credential profile checksum changed: %q", got)
	}
	var profile map[string]json.RawMessage
	if err := decodeExactJSONObject(
		encoded,
		&profile,
		[]string{
			"protocol_version",
			"endpoint",
			"method",
			"media_type",
			"serialization",
			"envelope_fields",
			"protected_header_fields",
			"protected_header_constants",
			"public_jwk_fields",
			"payload_fields",
			"payload_constants",
			"operations",
			"server_nonce_bytes",
			"minimum_jti_bytes",
			"maximum_proof_lifetime_seconds",
			"response",
			"revocation",
		},
	); err != nil {
		t.Fatalf("decodeExactJSONObject(credential profile) error = %v", err)
	}
	var version string
	if err := json.Unmarshal(profile["protocol_version"], &version); err != nil {
		t.Fatalf("Unmarshal(protocol version) error = %v", err)
	}
	var operations []string
	if err := json.Unmarshal(profile["operations"], &operations); err != nil {
		t.Fatalf("Unmarshal(operations) error = %v", err)
	}
	var proofLifetime int64
	if err := json.Unmarshal(
		profile["maximum_proof_lifetime_seconds"],
		&proofLifetime,
	); err != nil {
		t.Fatalf("Unmarshal(maximum proof lifetime) error = %v", err)
	}
	if version != ProtocolVersion ||
		len(operations) != 2 ||
		operations[0] != credentialStatusOperation ||
		operations[1] != credentialRevokeOperation ||
		proofLifetime != int64(DefaultLimits().MaxProofLifetime.Seconds()) {
		t.Fatalf(
			"credential profile version=%q operations=%q lifetime=%d",
			version,
			operations,
			proofLifetime,
		)
	}
}
