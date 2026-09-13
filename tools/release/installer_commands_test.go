package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The fake owns only the bootstrap command boundary. The signed installer and
// actual runtime are exercised by releaseclient.TestPublicMCPCompleteBundle.
func installerCommandFixture(t *testing.T, root string) []byte {
	t.Helper()
	source := `package main
import("encoding/json"; "os"; "path/filepath"; "runtime")
func main() {
 f,e:=os.OpenFile(os.Getenv("KADO_TEST_LOG"),os.O_CREATE|os.O_WRONLY|os.O_APPEND,0600);if e!=nil {panic(e)}
 json.NewEncoder(f).Encode(os.Args[1:]);f.Close()
 if len(os.Args)==3 && os.Args[1]=="version" {json.NewEncoder(os.Stdout).Encode(map[string]any{"schema_version":"kado.version.v2","kado":map[string]string{"version":"1.2.3","target":runtime.GOOS+"/"+runtime.GOARCH}})}
 if len(os.Args)>1 && os.Args[1]=="__install-bundle" {
  if len(os.Args)!=6 || os.Args[2]!="--directory" || os.Args[4]!="--target" {os.Exit(2)}
  self,_:=os.Executable(); b,e:=os.ReadFile(self);if e!=nil {panic(e)}
  if e=os.MkdirAll(filepath.Dir(os.Args[5]),0700);e!=nil {panic(e)}
  if e=os.WriteFile(os.Args[5],b,0700);e!=nil {panic(e)}
 }
}
`
	file := filepath.Join(root, "fixture.go")
	if err := os.WriteFile(file, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "fixture-bin")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	if out, err := exec.Command("go", "build", "-o", binary, file).CombinedOutput(); err != nil {
		t.Fatalf("fixture: %v %s", err, out)
	}
	b, err := os.ReadFile(binary)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestGeneratedInstallerDelegatesCompleteBundleAndFinishesSetup(t *testing.T) {
	root := t.TempDir()
	fixture := filepath.Join(root, "download")
	destination := filepath.Join(root, "install space")
	if err := os.Mkdir(fixture, 0700); err != nil {
		t.Fatal(err)
	}
	b := installerCommandFixture(t, root)
	suffix, format := "", ".tar.gz"
	if runtime.GOOS == "windows" {
		suffix, format = ".exe", ".zip"
	}
	base := "kado_1.2.3_" + runtime.GOOS + "_" + runtime.GOARCH
	for name, value := range map[string][]byte{base + suffix: b, base + format: []byte("archive passed to verifier"), "release-metadata.json": []byte("{\"schema_version\":\"kado.release.v3\",\"product\":\"kado\",\"version\":\"1.2.3\",\"components\":{\"mcp\":{\"version\":\"0.1.0\"}}}\n"), "release-metadata.json.sig": []byte("fixture-signature")} {
		if err := os.WriteFile(filepath.Join(fixture, name), value, 0700); err != nil {
			t.Fatal(err)
		}
	}
	log := filepath.Join(root, "commands.jsonl")
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		script := filepath.Join(root, "install.ps1")
		if err := os.WriteFile(script, []byte(installPowerShellScript(releaseIdentity{InstallURL: "https://fixture.invalid"}, "unused")), 0600); err != nil {
			t.Fatal(err)
		}
		c = exec.Command("powershell.exe", "-NoProfile", "-Command", `function Invoke-WebRequest {param([switch]$UseBasicParsing,[string]$Uri,[string]$OutFile); Copy-Item -LiteralPath (Join-Path $env:KADO_INSTALL_FIXTURE ([IO.Path]::GetFileName(([uri]$Uri).AbsolutePath))) -Destination $OutFile}; & $env:KADO_INSTALL_SCRIPT -InstallDirectory $env:KADO_INSTALL_DIR -NoModifyPath`)
		c.Env = append(os.Environ(), "KADO_INSTALL_SCRIPT="+script)
	} else {
		tools := filepath.Join(root, "tools")
		os.Mkdir(tools, 0700)
		fake := "#!/bin/sh\nfor arg in \"$@\"; do case \"$arg\" in https://*) url=\"$arg\" ;; esac; last=\"$arg\"; done\ncp \"$KADO_INSTALL_FIXTURE/${url##*/}\" \"$last\"\n"
		os.WriteFile(filepath.Join(tools, "curl"), []byte(fake), 0700)
		c = exec.Command("sh", "-c", installUnixScript(releaseIdentity{InstallURL: "https://fixture.invalid"}, "unused"))
		c.Env = append(os.Environ(), "PATH="+tools+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	c.Env = append(c.Env, "KADO_INSTALL_FIXTURE="+fixture, "KADO_INSTALL_DIR="+destination, "KADO_NO_MODIFY_PATH=1", "KADO_TEST_LOG="+log)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("installer: %v %s", err, out)
	}
	commands, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	text := string(commands)
	previous := -1
	for _, want := range []string{`["version","--json"]`, `["__install-bundle","--directory"`, `["skill","install"]`, `["auth","create"]`, `["auth","status"]`} {
		i := strings.Index(text, want)
		if i <= previous {
			t.Fatalf("missing/out-of-order %s: %s", want, text)
		}
		previous = i
	}
	installed, err := os.ReadFile(filepath.Join(destination, "kado"+suffix))
	if err != nil || string(installed) != string(b) {
		t.Fatal("bootstrap did not publish delegated result", err)
	}
}

func TestGeneratedUninstallerDelegatesWithoutDeletingFiles(t *testing.T) {
	root := t.TempDir()
	b := installerCommandFixture(t, root)
	suffix := ""
	if runtime.GOOS == "windows" {
		suffix = ".exe"
	}
	destination := filepath.Join(root, "kado"+suffix)
	os.WriteFile(destination, b, 0700)
	log := filepath.Join(root, "commands.jsonl")
	var c *exec.Cmd
	if runtime.GOOS == "windows" {
		script := filepath.Join(root, "uninstall.ps1")
		os.WriteFile(script, []byte(uninstallPowerShellScript()), 0600)
		c = exec.Command("powershell.exe", "-NoProfile", "-File", script, "-Yes", "-Destination", destination)
	} else {
		c = exec.Command("sh", "-c", uninstallUnixScript(), "uninstall.sh", "--yes")
	}
	c.Env = append(os.Environ(), "KADO_TEST_LOG="+log, "KADO_INSTALL_PATH="+destination)
	if out, err := c.CombinedOutput(); err != nil {
		t.Fatalf("uninstaller: %v %s", err, out)
	}
	commands, err := os.ReadFile(log)
	if err != nil || string(commands) != "[\"uninstall\",\"--yes\"]\n" {
		t.Fatalf("uninstall arguments: %s %v", commands, err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatal("script bypassed owned CLI removal", err)
	}
}
