// Package requestmeta attaches bounded installation metadata to enrollment.
package requestmeta

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"os/user"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	HeaderInstallation = "Kado-Installation-Metadata"
	maxLocalValueSize  = 254
)

type installationMetadata struct {
	Hostname      string `json:"hostname,omitempty"`
	LocalUsername string `json:"local_username,omitempty"`
}

// Transport adds hostname and local username to enrollment requests.
type Transport struct {
	Base                 http.RoundTripper
	installationMetadata string
}

// NewTransport captures the local hostname and username once.
func NewTransport(base http.RoundTripper) *Transport {
	hostname, _ := os.Hostname()
	username := ""
	if current, err := user.Current(); err == nil {
		username = current.Username
	}
	return newTransport(base, hostname, username)
}

func newTransport(base http.RoundTripper, hostname, username string) *Transport {
	if base == nil {
		base = http.DefaultTransport
	}
	installation, _ := json.Marshal(installationMetadata{
		Hostname:      boundedLocalValue(hostname),
		LocalUsername: boundedLocalValue(username),
	})
	return &Transport{
		Base:                 base,
		installationMetadata: base64.RawURLEncoding.EncodeToString(installation),
	}
}

func (transport *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	if strings.HasSuffix(cloned.URL.Path, "/api/auth/agent/enroll") {
		cloned.Header.Set(HeaderInstallation, transport.installationMetadata)
	} else {
		cloned.Header.Del(HeaderInstallation)
	}
	return transport.Base.RoundTrip(cloned)
}

func boundedLocalValue(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Map(func(character rune) rune {
		if unicode.IsControl(character) {
			return -1
		}
		return character
	}, value)
	if len(value) > maxLocalValueSize {
		value = value[:maxLocalValueSize]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}
