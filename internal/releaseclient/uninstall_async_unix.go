//go:build !windows

package releaseclient

import "github.com/kado-so/search/internal/buildinfo"

func StartCompleteUninstall(target string, _ buildinfo.Info) error  { return Uninstall(target) }
func RunCompleteUninstallHelper(_ []string, _ buildinfo.Info) error { return ErrUninstall }
