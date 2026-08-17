// Package buildinfo owns the metadata stamped into release binaries.
package buildinfo

import (
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
)

const maxMetadataRunes = 48

// These values are overridden at build time with -ldflags -X.
var (
	Version            = "dev"
	Commit             = "unknown"
	Date               = "unknown"
	Target             = "unknown"
	ReleasePublicKey   = ""
	ReleaseKeyID       = "unknown"
	ReleaseMetadataURL = ""
	InstallChannel     = "unknown"
)

// Info is the safe, bounded build metadata exposed by the CLI.
type Info struct {
	Version            string `json:"version"`
	Commit             string `json:"commit"`
	Date               string `json:"built_at"`
	Target             string `json:"target"`
	ReleaseKeyID       string `json:"release_key_id"`
	ReleasePublicKey   string `json:"-"`
	ReleaseMetadataURL string `json:"-"`
	InstallChannel     string `json:"-"`
}

// Current returns the metadata attached to this binary.
func Current() Info {
	return Info{
		Version:            boundedToken(Version),
		Commit:             boundedToken(Commit),
		Date:               boundedToken(Date),
		Target:             targetValue(Target),
		ReleaseKeyID:       boundedTokenLength(ReleaseKeyID, 80),
		ReleasePublicKey:   strings.TrimSpace(ReleasePublicKey),
		ReleaseMetadataURL: strings.TrimSpace(ReleaseMetadataURL),
		InstallChannel:     boundedToken(InstallChannel),
	}
}

// Line returns the single-line, human-readable version representation.
func (info Info) Line() string {
	return fmt.Sprintf(
		"kado %s commit=%s built=%s",
		boundedToken(info.Version),
		boundedToken(info.Commit),
		boundedToken(info.Date),
	)
}

// JSON returns deterministic, non-secret executable provenance.
func (info Info) JSON() ([]byte, error) {
	value := struct {
		Version      string `json:"version"`
		Commit       string `json:"commit"`
		BuiltAt      string `json:"built_at"`
		Target       string `json:"target"`
		ReleaseKeyID string `json:"release_key_id"`
		PublicKey    string `json:"release_public_key"`
	}{
		Version:      boundedToken(info.Version),
		Commit:       boundedToken(info.Commit),
		BuiltAt:      boundedToken(info.Date),
		Target:       targetValue(info.Target),
		ReleaseKeyID: boundedTokenLength(info.ReleaseKeyID, 80),
		PublicKey:    boundedTokenLength(info.ReleasePublicKey, 64),
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(encoded, '\n'), nil
}

func targetValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" || value == "unknown" {
		return runtime.GOOS + "/" + runtime.GOARCH
	}
	return boundedToken(value)
}

func boundedToken(value string) string {
	return boundedTokenLength(value, maxMetadataRunes)
}

func boundedTokenLength(value string, limit int) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}

	output := make([]rune, 0, min(len([]rune(value)), limit))
	for _, character := range value {
		if len(output) == limit {
			break
		}
		if character < '!' || character > '~' {
			output = append(output, '?')
			continue
		}
		output = append(output, character)
	}
	return string(output)
}
