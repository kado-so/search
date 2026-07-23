// Package buildinfo owns the metadata stamped into release binaries.
package buildinfo

import (
	"fmt"
	"strings"
)

const maxMetadataRunes = 48

// These values are overridden at build time with -ldflags -X.
var (
	Version = "dev"
	Commit  = "unknown"
	Date    = "unknown"
)

// Info is the safe, bounded build metadata exposed by the CLI.
type Info struct {
	Version string
	Commit  string
	Date    string
}

// Current returns the metadata attached to this binary.
func Current() Info {
	return Info{
		Version: boundedToken(Version),
		Commit:  boundedToken(Commit),
		Date:    boundedToken(Date),
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

func boundedToken(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}

	output := make([]rune, 0, min(len([]rune(value)), maxMetadataRunes))
	for _, character := range value {
		if len(output) == maxMetadataRunes {
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
