//go:build windows

package main

import (
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kado-so/search/internal/installchannel"
)

func TestGeneratedWinGetManifestPassesInstalledValidator(t *testing.T) {
	winget, err := exec.LookPath("winget")
	if err != nil {
		t.Skip("WinGet is unavailable")
	}
	output := packageFixture(t)
	if err := writePackageDefinitions(
		output,
		releaseIdentity{Version: "1.2.3", InstallURL: "https://kado.so/install"},
		installchannel.WinGet,
	); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(winget, "validate", "--manifest", filepath.Join(output, "manifests"))
	if result, err := command.CombinedOutput(); err != nil {
		t.Fatalf("winget validate: %v output=%q", err, result)
	}
}
