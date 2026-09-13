// Package payload owns the closed, signed format shared by release building,
// installation and launch. It never reads credentials or runs payload code.
package payload

import (
	"bytes"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"path"
	"regexp"
	"sort"
	"strings"
)

const (
	Schema        = "kado.payload.v1"
	ManifestName  = "bundle.gen.json"
	SignatureName = "bundle.gen.json.sig"
	MaxManifest   = 8 << 20
	MaxFile       = 256 << 20
	MaxExpanded   = 768 << 20
	MaxArchive    = 384 << 20
	MaxFiles      = 20000
)

var ErrInvalid = errors.New("invalid or incomplete Kado payload")
var digestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
var versionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`)

type File struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Mode   uint32 `json:"mode"`
}

// Component identifies the exact inputs, including the dependency lock and
// runtime distribution, used to build MCP. File inventory covers native addons.
type Component struct {
	Version           string `json:"version"`
	Commit            string `json:"commit"`
	LockSHA256        string `json:"lock_sha256"`
	NodeVersion       string `json:"node_version"`
	NodeArchiveSHA256 string `json:"node_archive_sha256"`
}

type Manifest struct {
	Schema  string            `json:"schema"`
	Version string            `json:"version"`
	Target  string            `json:"target"`
	MCP     Component         `json:"mcp"`
	Entries map[string]string `json:"entries"`
	Files   []File            `json:"files"`
}

type Bundle struct {
	Manifest  Manifest
	Encoded   []byte
	Signature []byte
	Files     map[string][]byte
}

func Digest(b []byte) string     { d := sha256.Sum256(b); return hex.EncodeToString(d[:]) }
func ValidDigest(s string) bool  { return digestPattern.MatchString(s) }
func ValidVersion(s string) bool { return len(s) <= 48 && versionPattern.MatchString(s) }

// ValidPath applies the portable subset on every host, including Windows device
// names, ADS, case collisions and trailing-dot aliases in Unix-created archives.
func ValidPath(s string) bool {
	if s == "" || len(s) > 240 || strings.ContainsAny(s, "\\:\x00<>\"|?*\r\n") || strings.HasPrefix(s, "/") {
		return false
	}
	for _, part := range strings.Split(s, "/") {
		if part == "" || part == "." || part == ".." || strings.TrimRight(part, " .") != part {
			return false
		}
		for _, r := range part {
			if r < 32 || r == 127 {
				return false
			}
		}
		base := strings.ToUpper(strings.Split(part, ".")[0])
		if base == "CON" || base == "PRN" || base == "AUX" || base == "NUL" || (len(base) == 4 && (strings.HasPrefix(base, "COM") || strings.HasPrefix(base, "LPT")) && base[3] >= '0' && base[3] <= '9') {
			return false
		}
	}
	return true
}

func Entries(target string) map[string]string {
	suffix := ""
	if strings.HasPrefix(target, "windows/") {
		suffix = ".exe"
	}
	return map[string]string{
		"kado": "kado" + suffix, "a2a": "kado-a2a" + suffix,
		"node": "mcp/runtime/node" + suffix, "mcp": "mcp/app/dist/cli/index.js",
		"bridge": "mcp/app/dist/bridge/index.js", "maintenance": "mcp/app/dist/maintenance/index.js",
		"host": "mcp/app/dist/native/kado-mcp-host" + suffix,
	}
}

func (m Manifest) Validate() error {
	if m.Schema != Schema || !ValidVersion(m.Version) || len(m.Files) == 0 || len(m.Files) > MaxFiles {
		return ErrInvalid
	}
	switch m.Target {
	case "windows/amd64", "windows/arm64", "linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64":
	default:
		return ErrInvalid
	}
	if !ValidVersion(m.MCP.Version) || !regexp.MustCompile(`^[a-f0-9]{40}$`).MatchString(m.MCP.Commit) || !ValidDigest(m.MCP.LockSHA256) || !ValidVersion(m.MCP.NodeVersion) || !ValidDigest(m.MCP.NodeArchiveSHA256) {
		return ErrInvalid
	}
	expected := Entries(m.Target)
	if len(m.Entries) != len(expected) {
		return ErrInvalid
	}
	for role, p := range expected {
		if m.Entries[role] != p {
			return ErrInvalid
		}
	}
	seen := map[string]File{}
	dirs := map[string]string{}
	var total int64
	previous := ""
	for _, f := range m.Files {
		folded := strings.ToLower(f.Path)
		if !ValidPath(f.Path) || f.Path <= previous || folded == ManifestName || folded == SignatureName || f.Size < 0 || f.Size > MaxFile || !ValidDigest(f.SHA256) || (f.Mode != 0644 && f.Mode != 0755) {
			return ErrInvalid
		}
		if _, ok := seen[folded]; ok {
			return ErrInvalid
		}
		seen[folded] = f
		for d := path.Dir(f.Path); d != "."; d = path.Dir(d) {
			lower := strings.ToLower(d)
			if old, ok := dirs[lower]; ok && old != d {
				return ErrInvalid
			}
			dirs[lower] = d
		}
		total += f.Size
		if total > MaxExpanded {
			return ErrInvalid
		}
		previous = f.Path
	}
	for d := range dirs {
		if _, ok := seen[d]; ok {
			return ErrInvalid
		}
	}
	for role, p := range expected {
		f, ok := seen[strings.ToLower(p)]
		if !ok || f.Path != p || f.Size == 0 {
			return ErrInvalid
		}
		wantMode := uint32(0644)
		if role == "kado" || role == "a2a" || role == "node" || role == "host" {
			wantMode = 0755
		}
		if f.Mode != wantMode {
			return ErrInvalid
		}
	}
	return nil
}

func Encode(m Manifest) ([]byte, error) {
	if err := m.Validate(); err != nil {
		return nil, err
	}
	b, err := json.Marshal(m)
	if err != nil || len(b)+1 > MaxManifest {
		return nil, ErrInvalid
	}
	return append(b, '\n'), nil
}

func Decode(encoded []byte) (Manifest, error) {
	var m Manifest
	if len(encoded) == 0 || len(encoded) > MaxManifest {
		return m, ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(encoded))
	d.DisallowUnknownFields()
	if d.Decode(&m) != nil || d.Decode(new(any)) != io.EOF {
		return m, ErrInvalid
	}
	canonical, err := Encode(m)
	if err != nil || !bytes.Equal(encoded, canonical) {
		return m, ErrInvalid
	}
	return m, nil
}

func Authenticate(encoded, signature []byte, key ed25519.PublicKey) (Manifest, error) {
	if len(encoded) > MaxManifest || len(key) != ed25519.PublicKeySize || len(signature) != ed25519.SignatureSize || !ed25519.Verify(key, encoded, signature) {
		return Manifest{}, ErrInvalid
	}
	return Decode(encoded)
}

// Seal is a release-builder operation; callers supply independently qualified
// component inputs and the exact target's complete file set.
func Seal(m Manifest, files map[string][]byte, key ed25519.PrivateKey) (Bundle, error) {
	if len(key) != ed25519.PrivateKeySize {
		return Bundle{}, ErrInvalid
	}
	m.Schema = Schema
	m.Entries = Entries(m.Target)
	m.Files = nil
	executables := map[string]bool{}
	for _, role := range []string{"kado", "a2a", "node", "host"} {
		executables[m.Entries[role]] = true
	}
	for p, b := range files {
		mode := uint32(0644)
		if executables[p] {
			mode = 0755
		}
		m.Files = append(m.Files, File{Path: p, Size: int64(len(b)), SHA256: Digest(b), Mode: mode})
	}
	sort.Slice(m.Files, func(i, j int) bool { return m.Files[i].Path < m.Files[j].Path })
	encoded, err := Encode(m)
	if err != nil {
		return Bundle{}, err
	}
	return Bundle{Manifest: m, Encoded: encoded, Signature: ed25519.Sign(key, encoded), Files: files}, nil
}
