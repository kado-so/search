package agentauth

import (
	"encoding/json"
	"testing"
)

func FuzzExactAgentAuthJSON(f *testing.F) {
	f.Add([]byte(`{"alg":"EdDSA","kid":"scred_00000000000000000000000000000001","typ":"JWT"}`))
	f.Add([]byte(`{"alg":"EdDSA","alg":"none"}`))
	f.Add([]byte(`null`))

	f.Fuzz(func(t *testing.T, encoded []byte) {
		if len(encoded) > 64*1024 {
			t.Skip()
		}
		var destination map[string]json.RawMessage
		_ = decodeExactJSONObject(
			encoded,
			&destination,
			[]string{"alg", "kid", "typ"},
		)
	})
}

func FuzzAdmissionParameterBounds(f *testing.F) {
	f.Add(uint32(8), uint32(1), uint8(1), "AAAAAAAAAAAAAAAAAAAAAA")
	f.Add(uint32(128*1024+1), uint32(5), uint8(5), "")

	f.Fuzz(func(
		t *testing.T,
		memory uint32,
		passes uint32,
		parallelism uint8,
		salt string,
	) {
		if len(salt) > 256 {
			t.Skip()
		}
		_ = validateAdmissionParameters(
			admissionChallenge{
				Algorithm:    argonAlgorithm,
				ArgonVersion: argonVersion,
				Salt:         salt,
				MemoryKiB:    memory,
				Passes:       passes,
				Parallelism:  parallelism,
				TagLength:    argonTagLength,
				CounterMode:  argonCounterMode,
				KeyPrefix:    "AAAAAAAAAAAAAAAAAAAAAA",
				KeySignature: "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
			},
			DefaultLimits(),
		)
	})
}
