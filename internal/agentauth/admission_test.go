package agentauth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"
)

type admissionProfileFixture struct {
	Profile         string          `json:"profile"`
	BindingEncoding json.RawMessage `json:"binding_encoding"`
	Vector          struct {
		Input struct {
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
		} `json:"input"`
		BindingBase64URL string `json:"binding_base64url"`
		BindingSHA256    string `json:"binding_sha256"`
		Parameters       struct {
			Algorithm    string `json:"algorithm"`
			ArgonVersion int    `json:"argon2_version"`
			Salt         string `json:"salt"`
			MemoryKiB    uint32 `json:"memory_kib"`
			Passes       uint32 `json:"passes"`
			Parallelism  uint8  `json:"parallelism"`
			TagLength    int    `json:"tag_length"`
			CounterMode  string `json:"counter_mode"`
		} `json:"parameters"`
		TargetCounter uint32 `json:"target_counter"`
		Challenge     struct {
			KeyPrefix    string `json:"key_prefix"`
			KeySignature string `json:"key_signature"`
		} `json:"challenge"`
		Solution struct {
			Counter    string `json:"counter"`
			DerivedKey string `json:"derived_key"`
		} `json:"solution"`
		HMACSecretBase64URL string `json:"hmac_secret_base64url"`
	} `json:"vector"`
}

func TestPinnedAdmissionProfileAndArgon2IDSolution(t *testing.T) {
	t.Parallel()

	encoded, err := os.ReadFile("testdata/admission-profile.v0.1.json")
	if err != nil {
		t.Fatalf("ReadFile(admission profile) error = %v", err)
	}
	digest := sha256.Sum256(encoded)
	if got := hex.EncodeToString(digest[:]); got !=
		"352f9e5072b25ed43affe29aa0670c52234f12954c6bbe31d4defe2520c6df16" {
		t.Fatalf("admission profile checksum changed: %q", got)
	}
	var fixture admissionProfileFixture
	if err := decodeStrictJSON(encoded, &fixture, true); err != nil {
		t.Fatalf("decodeStrictJSON(admission profile) error = %v", err)
	}
	input := fixtureBindingInput(fixture)
	binding, err := encodeAdmissionBinding(input)
	if err != nil {
		t.Fatalf("encodeAdmissionBinding() error = %v", err)
	}
	if got := rawBase64URL.EncodeToString(binding); got != fixture.Vector.BindingBase64URL {
		t.Fatalf("binding = %q", got)
	}
	bindingDigest := sha256.Sum256(binding)
	if got := hex.EncodeToString(bindingDigest[:]); got != fixture.Vector.BindingSHA256 {
		t.Fatalf("binding SHA-256 = %q", got)
	}
	challenge := fixtureChallenge(fixture)
	solution, err := solveAdmission(context.Background(), binding, challenge, DefaultLimits())
	if err != nil {
		t.Fatalf("solveAdmission() error = %v", err)
	}
	defer clear(solution.derivedKey)
	if solution.counter != fixture.Vector.TargetCounter ||
		rawBase64URL.EncodeToString(solution.derivedKey) != fixture.Vector.Solution.DerivedKey {
		t.Fatalf(
			"solution counter=%d key=%q",
			solution.counter,
			rawBase64URL.EncodeToString(solution.derivedKey),
		)
	}
}

func TestAdmissionBindingMutationsAndResourceLimitsFailClosed(t *testing.T) {
	t.Parallel()

	fixture := loadAdmissionFixture(t)
	original := fixtureBindingInput(fixture)
	binding, err := encodeAdmissionBinding(original)
	if err != nil {
		t.Fatalf("encodeAdmissionBinding() error = %v", err)
	}
	mutations := []admissionBindingInput{
		mutateBinding(original, func(value *admissionBindingInput) { value.ProtocolVersion = "0.2" }),
		mutateBinding(original, func(value *admissionBindingInput) { value.Operation = "login" }),
		mutateBinding(original, func(value *admissionBindingInput) { value.Issuer = "https://other.kado.so" }),
		mutateBinding(original, func(value *admissionBindingInput) { value.Endpoint += "/other" }),
		mutateBinding(original, func(value *admissionBindingInput) { value.TransactionID = "atx_" + string(make([]byte, 32)) }),
		mutateBinding(original, func(value *admissionBindingInput) { value.ExpiresAt++ }),
		mutateBinding(original, func(value *admissionBindingInput) { value.ManagementThumbprint = "C" + value.ManagementThumbprint[1:] }),
		mutateBinding(original, func(value *admissionBindingInput) { value.SessionThumbprint = "D" + value.SessionThumbprint[1:] }),
		mutateBinding(original, func(value *admissionBindingInput) { value.RequestedScopes = []string{"search:other"} }),
		mutateBinding(original, func(value *admissionBindingInput) { value.Audience = "https://api.kado.so" }),
		mutateBinding(original, func(value *admissionBindingInput) { value.ServerNonce = "changed" }),
		mutateBinding(original, func(value *admissionBindingInput) { value.ClientNonce = "changed" }),
		mutateBinding(original, func(value *admissionBindingInput) { value.CreateIfMissing = false }),
	}
	for index, mutation := range mutations {
		changed, err := encodeAdmissionBinding(mutation)
		if err != nil {
			t.Fatalf("encodeAdmissionBinding(mutation %d) error = %v", index, err)
		}
		if string(changed) == string(binding) {
			t.Fatalf("mutation %d did not alter canonical binding", index)
		}
	}

	challenge := fixtureChallenge(fixture)
	for name, mutate := range map[string]func(*admissionChallenge){
		"memory": func(value *admissionChallenge) { value.MemoryKiB = 129 * 1024 },
		"passes": func(value *admissionChallenge) { value.Passes = 5 },
		"parallelism": func(value *admissionChallenge) {
			value.Parallelism = 5
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := challenge
			mutate(&changed)
			if _, err := solveAdmission(
				context.Background(),
				binding,
				changed,
				DefaultLimits(),
			); !errors.Is(err, ErrChallengeLimits) {
				t.Fatalf("solveAdmission(%s) error = %v", name, err)
			}
		})
	}
	limits := DefaultLimits()
	limits.MaxArgonAttempts = fixture.Vector.TargetCounter
	if _, err := solveAdmission(
		context.Background(),
		binding,
		challenge,
		limits,
	); !errors.Is(err, ErrChallengeLimits) {
		t.Fatalf("attempt-limited solve error = %v", err)
	}
	limits = DefaultLimits()
	limits.MaxArgonElapsed = time.Nanosecond
	if _, err := solveAdmission(
		context.Background(),
		binding,
		challenge,
		limits,
	); !errors.Is(err, ErrChallengeLimits) {
		t.Fatalf("elapsed-limited solve error = %v", err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := solveAdmission(
		cancelled,
		binding,
		challenge,
		DefaultLimits(),
	); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled solve error = %v", err)
	}
}

func loadAdmissionFixture(t *testing.T) admissionProfileFixture {
	t.Helper()
	encoded, err := os.ReadFile("testdata/admission-profile.v0.1.json")
	if err != nil {
		t.Fatalf("ReadFile(admission profile) error = %v", err)
	}
	var fixture admissionProfileFixture
	if err := json.Unmarshal(encoded, &fixture); err != nil {
		t.Fatalf("Unmarshal(admission profile) error = %v", err)
	}
	return fixture
}

func fixtureBindingInput(fixture admissionProfileFixture) admissionBindingInput {
	input := fixture.Vector.Input
	return admissionBindingInput{
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

func fixtureChallenge(fixture admissionProfileFixture) admissionChallenge {
	parameters := fixture.Vector.Parameters
	return admissionChallenge{
		Algorithm:    parameters.Algorithm,
		ArgonVersion: parameters.ArgonVersion,
		Salt:         parameters.Salt,
		MemoryKiB:    parameters.MemoryKiB,
		Passes:       parameters.Passes,
		Parallelism:  parameters.Parallelism,
		TagLength:    parameters.TagLength,
		CounterMode:  parameters.CounterMode,
		KeyPrefix:    fixture.Vector.Challenge.KeyPrefix,
		KeySignature: fixture.Vector.Challenge.KeySignature,
	}
}

func mutateBinding(
	input admissionBindingInput,
	mutate func(*admissionBindingInput),
) admissionBindingInput {
	result := input
	result.RequestedScopes = append([]string(nil), input.RequestedScopes...)
	mutate(&result)
	return result
}
