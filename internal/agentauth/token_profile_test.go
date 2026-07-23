package agentauth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"
)

func TestPinnedTokenProfile(t *testing.T) {
	t.Parallel()

	encoded, err := os.ReadFile("testdata/token-profile.v0.1.json")
	if err != nil {
		t.Fatalf("ReadFile(token profile) error = %v", err)
	}
	digest := sha256.Sum256(encoded)
	if got := hex.EncodeToString(digest[:]); got !=
		"ea61ef5375d9571d06a17a5deb2521006013e67fcd8160b6b83d1aceb2890287" {
		t.Fatalf("token profile checksum changed: %q", got)
	}
	var profile map[string]json.RawMessage
	if err := decodeExactJSONObject(
		encoded,
		&profile,
		[]string{
			"protocol_version",
			"session_authorization",
			"token_request",
			"client_assertion",
			"token_response",
			"access_token",
			"jwks",
		},
	); err != nil {
		t.Fatalf("decodeExactJSONObject(token profile) error = %v", err)
	}
	var version string
	if err := json.Unmarshal(profile["protocol_version"], &version); err != nil {
		t.Fatalf("Unmarshal(protocol version) error = %v", err)
	}
	var access struct {
		MaximumLifetimeSeconds int64 `json:"maximum_lifetime_seconds"`
	}
	if err := json.Unmarshal(profile["access_token"], &access); err != nil {
		t.Fatalf("Unmarshal(access token profile) error = %v", err)
	}
	if version != ProtocolVersion ||
		access.MaximumLifetimeSeconds != int64(DefaultLimits().MaxAccessTokenLifetime.Seconds()) {
		t.Fatalf(
			"token profile version=%q lifetime=%d",
			version,
			access.MaximumLifetimeSeconds,
		)
	}
}
