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
		"b7d598401ab664105b82c902ee74d5df9062e2ff44e3c5cdfa02d41a12b13e13" {
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
		MaximumLifetimeSeconds  int64  `json:"maximum_lifetime_seconds"`
		MaximumClockSkewSeconds int64  `json:"maximum_clock_skew_seconds"`
		NotBeforeRule           string `json:"not_before_rule"`
	}
	if err := json.Unmarshal(profile["access_token"], &access); err != nil {
		t.Fatalf("Unmarshal(access token profile) error = %v", err)
	}
	var jwks struct {
		MinimumPreviousKeyOverlapSeconds int64    `json:"minimum_previous_key_overlap_seconds"`
		PreviousKeyScheduleFields        []string `json:"previous_key_schedule_fields"`
		ScheduleMetadataExposedInJWKS    bool     `json:"schedule_metadata_exposed_in_jwks"`
		ExpiredPreviousKeysOmitted       bool     `json:"expired_previous_keys_omitted"`
	}
	if err := json.Unmarshal(profile["jwks"], &jwks); err != nil {
		t.Fatalf("Unmarshal(jwks profile) error = %v", err)
	}
	limits := DefaultLimits()
	if version != ProtocolVersion ||
		access.MaximumLifetimeSeconds != int64(limits.MaxAccessTokenLifetime.Seconds()) ||
		access.MaximumClockSkewSeconds != int64(limits.MaxClockSkew.Seconds()) ||
		access.NotBeforeRule != "nbf equals iat" ||
		len(jwks.PreviousKeyScheduleFields) != 4 ||
		jwks.ScheduleMetadataExposedInJWKS ||
		!jwks.ExpiredPreviousKeysOmitted ||
		jwks.MinimumPreviousKeyOverlapSeconds !=
			int64((limits.MaxAccessTokenLifetime+2*limits.MaxClockSkew).Seconds()) {
		t.Fatalf(
			"token profile version=%q lifetime=%d skew=%d overlap=%d",
			version,
			access.MaximumLifetimeSeconds,
			access.MaximumClockSkewSeconds,
			jwks.MinimumPreviousKeyOverlapSeconds,
		)
	}
}
