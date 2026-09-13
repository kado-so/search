// Package mcpdispatch owns the public MCP namespace and verified runtime launch.
package mcpdispatch

import (
	"crypto/ed25519"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/executablepath"
	"github.com/kado-so/search/internal/payload"
)

type request struct {
	args       []string
	completion bool
}

// requestFor recognizes only Kado's boundary, never MCP options or commands.
func requestFor(argv []string) (request, bool) {
	if len(argv) < 2 {
		return request{}, false
	}
	a := argv[1:]
	completion := a[0] == "__complete" || a[0] == "__completeNoDesc"
	marker := a[0]
	if completion {
		a = a[1:]
	}
	if len(a) > 0 && a[0] == "--agent" {
		if len(a) < 3 || a[1] == "" {
			return request{}, false
		}
		a = a[2:]
	} else if len(a) > 0 && strings.HasPrefix(a[0], "--agent=") {
		if a[0] == "--agent=" {
			return request{}, false
		}
		a = a[1:]
	}
	if len(a) == 0 {
		return request{}, false
	}
	if !completion && a[0] == "help" && len(a) > 1 && a[1] == "mcp" {
		return request{args: append(append([]string{}, a[2:]...), "--help")}, true
	}
	if a[0] != "mcp" {
		return request{}, false
	}
	a = append([]string{}, a[1:]...)
	if completion {
		a = append([]string{marker}, a...)
	}
	return request{args: a, completion: completion}, true
}

func Matches(argv []string) bool    { _, ok := requestFor(argv); return ok }
func Completion(argv []string) bool { r, ok := requestFor(argv); return ok && r.completion }

func Dispatch(info buildinfo.Info, argv []string, stdin io.Reader, stdout, stderr io.Writer) (int, bool) {
	r, ok := requestFor(argv)
	if !ok {
		return 0, false
	}
	fail := func() (int, bool) {
		if r.completion {
			fmt.Fprintln(stdout, ":1")
			return 0, true
		}
		fmt.Fprintln(stderr, "kado: verified MCP component is unavailable; repair the Kado installation [mcp_unavailable]")
		return 1, true
	}
	executable, err := os.Executable()
	if err != nil {
		return fail()
	}
	executable, err = executablepath.Resolve(executable)
	if err != nil || info.MCP == nil {
		return fail()
	}
	root := filepath.Dir(executable)
	key, err := base64.RawStdEncoding.DecodeString(info.ReleasePublicKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		return fail()
	}
	encoded, err := payload.ReadFile(root, payload.ManifestName, payload.MaxManifest)
	if err != nil {
		return fail()
	}
	m, err := payload.VerifyTree(root, payload.Digest(encoded), key)
	if err != nil || m.Version != info.Version || m.Target != runtime.GOOS+"/"+runtime.GOARCH || m.MCP != *info.MCP || executable != filepath.Join(root, m.Entries["kado"]) {
		return fail()
	}
	args := append([]string{"--no-global-search-paths", filepath.Join(root, m.Entries["mcp"])}, r.args...)
	code, err := run(filepath.Join(root, m.Entries["node"]), args, payload.NodeEnvironment(os.Environ()), stdin, stdout, stderr, r.completion)
	if err != nil {
		return fail()
	}
	return code, true
}
