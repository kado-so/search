package keystore

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
)

type verbosePrivateError struct {
	Secret string
}

func (verbosePrivateError) Error() string {
	return "private backend failure"
}

func TestRecordRoundTripCopiesOpaqueKeyMaterial(t *testing.T) {
	t.Parallel()

	keyMaterial := []byte("opaque-management-key-material")
	encoded, err := encodeRecord(keyMaterial)
	if err != nil {
		t.Fatalf("encodeRecord() error = %v", err)
	}
	decoded, err := decodeRecord(encoded)
	if err != nil {
		t.Fatalf("decodeRecord() error = %v", err)
	}
	if string(decoded) != string(keyMaterial) {
		t.Fatalf("decoded key material differs")
	}
	decoded[0] = 'X'
	if keyMaterial[0] == 'X' {
		t.Fatal("decoded key material aliases caller input")
	}
}

func TestRecordRejectsCorruptionAndUnknownVersions(t *testing.T) {
	t.Parallel()

	validData := base64.RawStdEncoding.EncodeToString([]byte("key"))
	for _, encoded := range []string{
		"",
		"not json",
		`{"version":0,"data":"` + validData + `"}`,
		`{"version":2,"data":"` + validData + `"}`,
		`{"version":1,"data":""}`,
		`{"version":1,"data":"***"}`,
		`{"version":1,"data":"` + validData + `","unexpected":true}`,
		`{"version":1,"data":"` + validData + `"} {}`,
		`{"version":1,"data":"` + validData + `"} 42`,
	} {
		if _, err := decodeRecord(encoded); !errors.Is(err, ErrCorrupt) {
			t.Errorf("decodeRecord(%q) error = %v, want ErrCorrupt", encoded, err)
		}
	}
}

func TestStorageErrorsNeverRenderPrivateCause(t *testing.T) {
	t.Parallel()

	private := verbosePrivateError{Secret: "private-key-material=do-not-print"}
	err := storageError("load", ErrUnavailable, private)
	typed := err.(*Error)
	forms := []any{
		typed,
		*typed,
		fmt.Errorf("wrapped pointer: %w", typed),
		fmt.Errorf("wrapped value: %w", *typed),
	}
	for _, form := range forms {
		for _, format := range []string{"%v", "%+v", "%#v", "%s"} {
			rendered := fmt.Sprintf(format, form)
			if strings.Contains(rendered, private.Secret) {
				t.Fatalf("%s exposed private cause from %T: %q", format, form, rendered)
			}
		}
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatal("storage error lost safe classification")
	}
	if !errors.Is(err, private) {
		t.Fatal("storage error lost private cause for trusted inspection")
	}
}
