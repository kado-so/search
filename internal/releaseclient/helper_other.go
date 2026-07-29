//go:build !windows

package releaseclient

func RunWindowsUpdateHelper([]string) error {
	return ErrPlatform
}
