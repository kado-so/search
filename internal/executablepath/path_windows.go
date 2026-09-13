package executablepath

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows"
)

const maximumWindowsPathUTF16 = 32768

// Resolve returns the physical executable path behind a public shim or alias.
func Resolve(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	file, err := os.Open(absolute)
	if err != nil {
		return "", err
	}
	defer file.Close()

	buffer := make([]uint16, maximumWindowsPathUTF16)
	length, err := windows.GetFinalPathNameByHandle(
		windows.Handle(file.Fd()),
		&buffer[0],
		uint32(len(buffer)),
		0,
	)
	if err != nil || length == 0 || length >= uint32(len(buffer)) {
		return "", errors.New("final executable path is unavailable")
	}
	resolved := windows.UTF16ToString(buffer[:length])
	switch {
	case strings.HasPrefix(resolved, `\\?\UNC\`):
		resolved = `\\` + strings.TrimPrefix(resolved, `\\?\UNC\`)
	case strings.HasPrefix(resolved, `\\?\`):
		resolved = strings.TrimPrefix(resolved, `\\?\`)
	}
	return filepath.Clean(resolved), nil
}
