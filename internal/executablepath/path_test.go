package executablepath

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

func TestResolvePhysicalExecutableThroughDirectoryAlias(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	real := filepath.Join(root, "real space \u00fc")
	if err := os.Mkdir(real, 0700); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(real, "kado.exe")
	if err := os.WriteFile(want, []byte("fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias space")
	if runtime.GOOS == "windows" {
		c := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", `New-Item -ItemType Junction -Path $env:KADO_TEST_ALIAS -Target $env:KADO_TEST_TARGET -ErrorAction Stop | Out-Null`)
		c.Env = append(os.Environ(), "KADO_TEST_ALIAS="+alias, "KADO_TEST_TARGET="+real)
		if out, err := c.CombinedOutput(); err != nil {
			t.Fatalf("junction: %v %s", err, out)
		}
	} else if err := os.Symlink(real, alias); err != nil {
		t.Fatal(err)
	}
	defer os.Remove(alias)
	got, err := Resolve(filepath.Join(alias, "kado.exe"))
	if err != nil || got != want {
		t.Fatalf("physical path = %q, %v; want %q", got, err, want)
	}
}
