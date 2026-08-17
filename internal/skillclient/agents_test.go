package skillclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillDestinationsIncludeProductPathsAndPortableFallback(t *testing.T) {
	t.Parallel()
	home := filepath.Join("home", "test")
	tests := []struct {
		agent string
		root  string
	}{
		{agent: "codex", root: filepath.Join(".codex", "skills")},
		{agent: "gemini-cli", root: filepath.Join(".gemini", "skills")},
		{agent: "antigravity", root: filepath.Join(".gemini", "config", "skills")},
		{agent: "default", root: filepath.Join(".agents", "skills")},
		{agent: "devin", root: filepath.Join(".agents", "skills")},
		{agent: "hermes", root: filepath.Join(".agents", "skills")},
		{agent: "openhands", root: filepath.Join(".agents", "skills")},
	}
	for _, test := range tests {
		t.Run(test.agent, func(t *testing.T) {
			t.Parallel()
			got, err := Destination(home, test.agent)
			want := filepath.Join(home, test.root, SkillName)
			if err != nil || got != want {
				t.Fatalf("Destination(%q) = %q, %v; want %q", test.agent, got, err, want)
			}
		})
	}
}

func TestDefaultInstallUsesAllDetectedLocationsAndGeminiPair(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	for _, directory := range []string{
		filepath.Join(home, ".claude"),
		filepath.Join(home, ".gemini"),
	} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	manager := Manager{ConfigDir: filepath.Join(root, "config"), HomeDir: home, CurrentVersion: "dev"}
	result, err := manager.Install(context.Background(), InstallOptions{CurrentAgent: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{}
	for _, agent := range []string{"agents", "antigravity", "claude-code", "codex", "gemini-cli"} {
		for _, name := range []string{"kado-cli-non-search", SkillName} {
			want[agent+":"+name] = true
		}
	}
	for _, item := range result.Installed {
		delete(want, item.Agent+":"+item.Name)
	}
	if len(want) != 0 || len(result.Installed) != 10 {
		t.Fatalf("Install() = %#v; missing %#v", result.Installed, want)
	}
}

func TestStatusDiscoversVerifiedUnregisteredInstallation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manager := Manager{ConfigDir: filepath.Join(root, "config"), HomeDir: filepath.Join(root, "home"), CurrentVersion: "dev"}
	_, err := manager.Install(context.Background(), InstallOptions{Agents: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(manager.ConfigDir, registryName)); err != nil {
		t.Fatal(err)
	}
	status, err := manager.Status()
	if err != nil || len(status.Installations) != 2 {
		t.Fatalf("Status() = %#v, %v", status, err)
	}
}

func TestStatusDetectsContentReceiptAndDeletionDrift(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		mutate func(t *testing.T, item Installation)
		code   string
	}{
		{
			name: "content",
			mutate: func(t *testing.T, item Installation) {
				if err := os.WriteFile(filepath.Join(item.Path, "SKILL.md"), []byte("changed"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			code: "locally_modified",
		},
		{
			name: "receipt",
			mutate: func(t *testing.T, item Installation) {
				value, err := readReceipt(item.Path)
				if err != nil {
					t.Fatal(err)
				}
				value.Version = "9.9.9"
				if err := writeJSONAtomic(filepath.Join(item.Path, receiptName), value, 0o600); err != nil {
					t.Fatal(err)
				}
			},
			code: "externally_managed",
		},
		{
			name: "deleted",
			mutate: func(t *testing.T, item Installation) {
				if err := os.RemoveAll(item.Path); err != nil {
					t.Fatal(err)
				}
			},
			code: "externally_managed",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			manager := Manager{ConfigDir: filepath.Join(root, "config"), HomeDir: filepath.Join(root, "home"), CurrentVersion: "dev"}
			result, err := manager.Install(context.Background(), InstallOptions{Agents: []string{"codex"}})
			if err != nil {
				t.Fatal(err)
			}
			item := result.Installed[0]
			test.mutate(t, item)
			status, err := manager.Status()
			if err != nil || status.Failures[item.Path] != test.code {
				t.Fatalf("Status() = %#v, %v; want %q", status, err, test.code)
			}
		})
	}
}

func TestUninstallPreflightLeavesAllSkillsWhenOneCopyIsModified(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manager := Manager{ConfigDir: filepath.Join(root, "config"), HomeDir: filepath.Join(root, "home"), CurrentVersion: "dev"}
	result, err := manager.Install(context.Background(), InstallOptions{Agents: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Installed) != 2 {
		t.Fatalf("Install() installed %d skills, want 2", len(result.Installed))
	}
	modified := result.Installed[len(result.Installed)-1]
	if err := os.WriteFile(filepath.Join(modified.Path, "SKILL.md"), []byte("changed"), 0o644); err != nil {
		t.Fatal(err)
	}
	if removed, err := manager.Uninstall(nil, true); !errors.Is(err, ErrLocallyModified) || len(removed) != 0 {
		t.Fatalf("Uninstall() = %#v, %v; want no removals and ErrLocallyModified", removed, err)
	}
	for _, item := range result.Installed {
		if _, err := os.Stat(item.Path); err != nil {
			t.Fatalf("preflight failure removed %s: %v", item.Path, err)
		}
	}
	state, err := readRegistry(manager.ConfigDir)
	if err != nil || len(state.Installations) != len(result.Installed) {
		t.Fatalf("registry changed after failed uninstall: %#v, %v", state, err)
	}
}

func TestUpdatePropagatesToEveryManagedLocation(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	manager := Manager{ConfigDir: filepath.Join(root, "config"), HomeDir: filepath.Join(root, "home"), CurrentVersion: "dev"}
	result, err := manager.Install(context.Background(), InstallOptions{Agents: []string{"codex", "claude-code", "gemini-cli"}})
	if err != nil {
		t.Fatal(err)
	}
	state, err := readRegistry(manager.ConfigDir)
	if err != nil {
		t.Fatal(err)
	}
	for index := range state.Installations {
		state.Installations[index].Version = "0.0.1"
		receipt, err := readReceipt(state.Installations[index].Path)
		if err != nil {
			t.Fatal(err)
		}
		receipt.Version = "0.0.1"
		if err := writeJSONAtomic(filepath.Join(state.Installations[index].Path, receiptName), receipt, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeRegistry(manager.ConfigDir, state); err != nil {
		t.Fatal(err)
	}
	updated, err := manager.Update(context.Background())
	if err != nil || len(updated.Updated) != len(result.Installed) || len(updated.Failures) != 0 {
		t.Fatalf("Update() = %#v, %v; want %d updates", updated, err, len(result.Installed))
	}
}
