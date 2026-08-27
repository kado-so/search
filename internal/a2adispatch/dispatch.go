// Package a2adispatch delegates the public Kado A2A namespace to the bundled
// official A2A CLI.
package a2adispatch

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kado-so/search/internal/buildinfo"
)

const (
	namespace      = "a2a"
	maxSidecarSize = 96 << 20
)

// Dispatch delegates arguments that select the a2a namespace. It returns
// handled=false without writing output when the invocation belongs to Kado.
func Dispatch(
	info buildinfo.Info,
	arguments []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
) (code int, handled bool) {
	forwarded, ok := forwardedArguments(arguments)
	if !ok {
		return 0, false
	}

	executable, err := os.Executable()
	if err != nil {
		return failure(stderr, "bundled A2A CLI is unavailable")
	}
	sidecar, err := verifiedSidecar(
		executable,
		info.A2A.ArtifactSize,
		info.A2A.ArtifactSHA256,
	)
	if err != nil {
		return failure(stderr, "bundled A2A CLI is unavailable")
	}
	code, err = runSidecar(sidecar, forwarded, stdin, stdout, stderr)
	if err != nil {
		return failure(stderr, "bundled A2A CLI could not start")
	}
	return code, true
}

func forwardedArguments(arguments []string) ([]string, bool) {
	index, ok := commandIndex(arguments)
	if !ok {
		return nil, false
	}
	if arguments[index] == namespace {
		return arguments[index+1:], true
	}
	if arguments[index] != "help" || len(arguments) <= index+1 ||
		arguments[index+1] != namespace {
		return nil, false
	}
	if len(arguments) == index+2 {
		return []string{"--help"}, true
	}
	forwarded := make([]string, 1, len(arguments)-index-1)
	forwarded[0] = "help"
	forwarded = append(forwarded, arguments[index+2:]...)
	return forwarded, true
}

func commandIndex(arguments []string) (int, bool) {
	if len(arguments) < 2 {
		return 0, false
	}
	index := 1
	if arguments[index] == "--agent" {
		if len(arguments) <= index+2 || arguments[index+1] == "" {
			return 0, false
		}
		index += 2
	} else if strings.HasPrefix(arguments[index], "--agent=") {
		if arguments[index] == "--agent=" || len(arguments) <= index+1 {
			return 0, false
		}
		index++
	}
	return index, true
}

func verifiedSidecar(executable string, expectedSize int64, expectedDigest string) (string, error) {
	if expectedSize <= 0 || expectedSize > maxSidecarSize || !validDigest(expectedDigest) {
		return "", errors.New("sidecar identity is invalid")
	}
	executable, err := canonicalExecutablePath(executable)
	if err != nil || !filepath.IsAbs(executable) {
		return "", errors.New("executable path is unavailable")
	}

	sidecar := filepath.Join(filepath.Dir(executable), sidecarName())
	info, err := os.Lstat(sidecar)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&fs.ModeSymlink != 0 ||
		info.Size() != expectedSize {
		return "", errors.New("sidecar file is invalid")
	}
	file, err := os.Open(sidecar)
	if err != nil {
		return "", errors.New("sidecar file is unavailable")
	}
	hash := sha256.New()
	written, copyErr := io.Copy(hash, io.LimitReader(file, expectedSize+1))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil || written != expectedSize ||
		hex.EncodeToString(hash.Sum(nil)) != expectedDigest {
		return "", errors.New("sidecar checksum does not match")
	}
	return sidecar, nil
}

func validDigest(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func failure(stderr io.Writer, message string) (int, bool) {
	_, _ = fmt.Fprintf(stderr, "kado: %s [a2a_unavailable]\n", message)
	return 1, true
}

func sidecarName() string {
	if runtime.GOOS == "windows" {
		return "kado-a2a.exe"
	}
	return "kado-a2a"
}
