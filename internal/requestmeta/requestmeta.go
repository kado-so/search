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
	HeaderAgent        = "X-Kado-Agent"
	EnvironmentCohort  = "KADO_INSTALL_COHORT"
	maxLocalValueSize  = 254
)

type installationMetadata struct {
	CohortID      string `json:"cohort_id,omitempty"`
	HostID        string `json:"host_id"`
	Hostname      string `json:"hostname,omitempty"`
	LocalUsername string `json:"local_username,omitempty"`
	Agent         string `json:"agent"`
}

// Transport adds hostname and local username to enrollment requests.
type Transport struct {
	Base                 http.RoundTripper
	agent                string
	installationMetadata string
}

// NewTransport captures the local hostname and username once.
func NewTransport(base http.RoundTripper, agent, hostID string) *Transport {
	hostname, _ := os.Hostname()
	username := ""
	if current, err := user.Current(); err == nil {
		username = current.Username
	}
	return newTransport(base, agent, hostID, hostname, username, os.Getenv(EnvironmentCohort))
}

func newTransport(base http.RoundTripper, agent, hostID, hostname, username, cohortID string) *Transport {
	if base == nil {
		base = http.DefaultTransport
	}
	installation, _ := json.Marshal(installationMetadata{
		CohortID:      boundedCohortID(cohortID),
		HostID:        boundedLocalValue(hostID),
		Hostname:      boundedLocalValue(hostname),
		LocalUsername: boundedLocalValue(username),
		Agent:         boundedLocalValue(agent),
	})
	return &Transport{
		Base:                 base,
		agent:                agent,
		installationMetadata: base64.RawURLEncoding.EncodeToString(installation),
	}
}

func boundedCohortID(value string) string {
	value = strings.TrimSpace(value)
	if len(value) == 0 || len(value) > 64 {
		return ""
	}
	for index, character := range []byte(value) {
		if (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') ||
			(index > 0 && (character == '_' || character == '-')) {
			continue
		}
		return ""
	}
	return value
}

func (transport *Transport) RoundTrip(request *http.Request) (*http.Response, error) {
	cloned := request.Clone(request.Context())
	cloned.Header = request.Header.Clone()
	cloned.Header.Set(HeaderAgent, transport.agent)
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
