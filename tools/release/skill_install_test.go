package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kado-so/search/internal/releaseclient"
	"github.com/kado-so/search/internal/skillclient"
)

type skillArtifacts struct {
	files   map[string][]byte
	reads   []string
	offline bool
}

func (f *skillArtifacts) Fetch(_ context.Context, url string, maximum int64) ([]byte, error) {
	f.reads = append(f.reads, url)
	b, ok := f.files[url]
	if f.offline || !ok || int64(len(b)) > maximum {
		return nil, fmt.Errorf("unavailable fixture artifact")
	}
	return b, nil
}

// Exercise the production signer, archive builder, verifier and on-disk manager
// together. Only the HTTPS artifact transport is replaced with fixed bytes.
func TestSignedMCPSkillLifecycle(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	const base = "https://kado.so/install"
	set, err := makeSkillReleases(base, private)
	if err != nil {
		t.Fatal(err)
	}
	fetcher := &skillArtifacts{files: map[string][]byte{base + "/skills/latest/catalog.json": set.Catalog, base + "/skills/latest/catalog.json.sig": set.Signature}}
	for _, release := range set.Releases {
		var metadata skillclient.Metadata
		if err := json.Unmarshal(release.Metadata, &metadata); err != nil {
			t.Fatal(err)
		}
		url := strings.TrimSuffix(metadata.Archive.URL, release.Name+".tar.gz") + "metadata.json"
		fetcher.files[url], fetcher.files[url+".sig"], fetcher.files[metadata.Archive.URL] = release.Metadata, release.Signature, release.Archive
	}
	root := t.TempDir()
	manager := skillclient.Manager{ConfigDir: filepath.Join(root, "config"), HomeDir: filepath.Join(root, "home space ü"), BaseURL: "https://kado.so", PublicKey: releaseclient.PublicKeyText(public), CurrentVersion: "0.2.0", Fetcher: fetcher}
	installed, err := manager.Install(context.Background(), skillclient.InstallOptions{Agents: []string{"codex", "claude-code", "gemini-cli", "agents"}})
	if err != nil || installed.UsedFallback || len(installed.Failures) != 0 || len(installed.Installed) != 20 {
		t.Fatalf("signed install: %+v %v", installed, err)
	}
	_, embedded, err := skillclient.EmbeddedCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var mcpPath string
	for _, item := range installed.Installed {
		for path, want := range embedded[item.Name+":default"].Files {
			got, err := os.ReadFile(filepath.Join(item.Path, filepath.FromSlash(path)))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("installed %s/%s differs: %v", item.Name, path, err)
			}
		}
		if item.Name == "kado-mcp" && item.Agent == "codex" {
			mcpPath = item.Path
		}
	}
	status, err := manager.Status()
	if err != nil || len(status.Installations) != 20 || len(status.Failures) != 0 || mcpPath == "" {
		t.Fatalf("receipts: %+v %v", status, err)
	}
	updated, err := manager.Update(context.Background())
	if err != nil || len(updated.Failures) != 0 || len(updated.Current) != 20 {
		t.Fatalf("idempotent signed refresh: %+v %v", updated, err)
	}
	// Refresh MCP to a compatible signed version in every managed destination.
	var mcpURL string
	for url, encoded := range fetcher.files {
		if strings.Contains(url, "/kado-mcp/") && strings.HasSuffix(url, "metadata.json") {
			mcpURL = url
			var metadata skillclient.Metadata
			if err := json.Unmarshal(encoded, &metadata); err != nil {
				t.Fatal(err)
			}
			metadata.Version = "0.1.1"
			fetcher.files[url], err = skillclient.CanonicalMetadata(metadata)
			if err != nil {
				t.Fatal(err)
			}
			fetcher.files[url+".sig"] = ed25519.Sign(private, fetcher.files[url])
		}
	}
	if mcpURL == "" {
		t.Fatal("MCP signed metadata missing")
	}
	updated, err = manager.Update(context.Background())
	if err != nil || len(updated.Updated) != 5 || len(updated.Failures) != 0 {
		t.Fatalf("compatible signed refresh: %+v %v", updated, err)
	}
	for _, item := range updated.Updated {
		if item.Name != "kado-mcp" || item.Version != "0.1.1" {
			t.Fatalf("unexpected refresh: %+v", item)
		}
	}
	// Publish a validly signed future MCP release that this CLI cannot use.
	var future skillclient.Metadata
	if err := json.Unmarshal(fetcher.files[mcpURL], &future); err != nil {
		t.Fatal(err)
	}
	future.Version, future.MinimumCLIVersion = "0.2.0", "0.3.0"
	fetcher.files[mcpURL], err = skillclient.CanonicalMetadata(future)
	if err != nil {
		t.Fatal(err)
	}
	fetcher.files[mcpURL+".sig"] = ed25519.Sign(private, fetcher.files[mcpURL])
	fetcher.reads = nil
	updated, err = manager.Update(context.Background())
	if err != nil || updated.Failures[mcpPath] != "unsupported_cli" {
		t.Fatalf("future release accepted: %+v %v", updated, err)
	}
	for _, url := range fetcher.reads {
		if strings.Contains(url, "/kado-mcp/") && strings.HasSuffix(url, ".tar.gz") {
			t.Fatal("downloaded incompatible archive")
		}
	}
	status, err = manager.Status()
	if err != nil || len(status.Failures) != 0 || len(status.Installations) != 20 {
		t.Fatalf("incompatible update changed receipts: %+v %v", status, err)
	}
	fetcher.offline = true
	if _, err := manager.Update(context.Background()); err == nil {
		t.Fatal("offline signed refresh unexpectedly succeeded")
	}
	status, err = manager.Status()
	if err != nil || len(status.Installations) != 20 || len(status.Failures) != 0 {
		t.Fatalf("offline refresh damaged copies: %+v %v", status, err)
	}
	// Offline fresh installation uses the same versioned bundle and receipts.
	manager.ConfigDir, manager.HomeDir = filepath.Join(root, "offline-config"), filepath.Join(root, "offline-home")
	installed, err = manager.Install(context.Background(), skillclient.InstallOptions{Agents: []string{"codex"}})
	if err != nil || !installed.UsedFallback || len(installed.Installed) != 4 || len(installed.Failures) != 0 {
		t.Fatalf("offline install: %+v %v", installed, err)
	}
	for _, version := range []string{"0.1.22", "0.2.0-rc.1", "invalid", ""} {
		manager.CurrentVersion = version
		manager.ConfigDir, manager.HomeDir = filepath.Join(t.TempDir(), "config"), filepath.Join(t.TempDir(), "home")
		installed, err = manager.Install(context.Background(), skillclient.InstallOptions{Agents: []string{"codex"}})
		path, _ := skillclient.Destination(manager.HomeDir, "codex", "kado-mcp")
		if err != nil || installed.Failures[path] != "unsupported_cli" {
			t.Fatalf("offline floor %q: %+v %v", version, installed, err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("incompatible skill written: %v", err)
		}
	}
}
