package payload

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (Bundle, ed25519.PublicKey) {
	t.Helper()
	key := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{7}, 32))
	m := Manifest{Version: "1.0.0", Target: runtime.GOOS + "/" + runtime.GOARCH, MCP: Component{Version: "0.1.0-dev.0", Commit: strings.Repeat("a", 40), LockSHA256: strings.Repeat("b", 64), NodeVersion: "24.20.0", NodeArchiveSHA256: strings.Repeat("c", 64)}}
	files := map[string][]byte{}
	for role, p := range Entries(m.Target) {
		files[p] = []byte("fixture-" + role)
	}
	files["mcp/app/node_modules/example/native.node"] = []byte("native dependency")
	b, err := Seal(m, files, key)
	if err != nil {
		t.Fatal(err)
	}
	return b, key.Public().(ed25519.PublicKey)
}

func TestArchiveAndTree(t *testing.T) {
	b, key := fixture(t)
	for _, format := range []string{"zip", "tar.gz"} {
		t.Run(format, func(t *testing.T) {
			data, err := Archive(b, format, time.Unix(1700000000, 0).UTC())
			if err != nil {
				t.Fatal(err)
			}
			again, err := Archive(b, format, time.Unix(1700000000, 0).UTC())
			if err != nil || !bytes.Equal(data, again) {
				t.Fatal("archive is not reproducible")
			}
			got, err := Extract(data, format, key)
			if err != nil {
				t.Fatal(err)
			}
			root, err := filepath.EvalSymlinks(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := WriteTree(root, got, key); err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyTree(root, Digest(b.Encoded), key); err != nil {
				t.Fatal(err)
			}
			if _, err := Extract(data, format, ed25519.NewKeyFromSeed(make([]byte, 32)).Public().(ed25519.PublicKey)); err == nil {
				t.Fatal("accepted wrong signer")
			}
		})
	}
}

func TestEveryFileTamperAndMissing(t *testing.T) {
	b, key := fixture(t)
	for _, f := range append([]File{{Path: ManifestName}, {Path: SignatureName}}, b.Manifest.Files...) {
		for _, op := range []string{"change", "remove"} {
			t.Run(f.Path+"/"+op, func(t *testing.T) {
				root, _ := filepath.EvalSymlinks(t.TempDir())
				if err := WriteTree(root, b, key); err != nil {
					t.Fatal(err)
				}
				p := filepath.Join(root, filepath.FromSlash(f.Path))
				if op == "remove" {
					if err := os.Remove(p); err != nil {
						t.Fatal(err)
					}
				} else {
					v, err := os.ReadFile(p)
					if err != nil {
						t.Fatal(err)
					}
					v[0] ^= 1
					if err := os.WriteFile(p, v, 0644); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := VerifyTree(root, Digest(b.Encoded), key); err == nil {
					t.Fatal("accepted tampered tree")
				}
			})
		}
	}
}

func TestUnlistedAndLinkedTree(t *testing.T) {
	b, key := fixture(t)
	for _, kind := range []string{"file", "directory", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root, _ := filepath.EvalSymlinks(t.TempDir())
			if err := WriteTree(root, b, key); err != nil {
				t.Fatal(err)
			}
			p := filepath.Join(root, "extra")
			var err error
			switch kind {
			case "file":
				err = os.WriteFile(p, []byte("injection"), 0644)
			case "directory":
				err = os.Mkdir(p, 0755)
			case "symlink":
				err = os.Symlink(filepath.Join(root, ManifestName), p)
				if err != nil && runtime.GOOS == "windows" {
					t.Skip("symlink privilege unavailable")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := VerifyTree(root, Digest(b.Encoded), key); err == nil {
				t.Fatal("accepted extra entry")
			}
		})
	}
}

func TestPortablePathAndManifestRejection(t *testing.T) {
	for _, p := range []string{"../x", "/x", "a\\b", "C:x", "a:b", "a/CON.txt", "a/lpt1", "a./x", "a /x", "a//b", "a\x00b"} {
		if ValidPath(p) {
			t.Fatalf("accepted %q", p)
		}
	}
	b, _ := fixture(t)
	for _, kind := range []string{"duplicate", "case", "directory-case", "file-directory", "missing-role", "oversize", "unknown-role"} {
		t.Run(kind, func(t *testing.T) {
			m, err := Decode(b.Encoded)
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "duplicate":
				m.Files = append(m.Files, m.Files[0])
			case "case":
				f := m.Files[0]
				f.Path = strings.ToUpper(f.Path)
				m.Files = append(m.Files, f)
			case "directory-case":
				f := m.Files[0]
				f.Path = "MCP/extra"
				m.Files = append(m.Files, f)
			case "file-directory":
				f := m.Files[0]
				f.Path = "mcp"
				m.Files = append(m.Files, f)
			case "missing-role":
				m.Files = m.Files[1:]
			case "oversize":
				m.Files[0].Size = MaxFile + 1
			case "unknown-role":
				m.Entries["inject"] = "evil.js"
			}
			if _, err := Encode(m); err == nil {
				t.Fatal("accepted invalid manifest")
			}
		})
	}
}

func TestRejectMaliciousZip(t *testing.T) {
	b, key := fixture(t)
	for _, kind := range []string{"traversal", "duplicate", "link", "missing", "reordered", "excess", "mode", "manifest", "signature", "oversize"} {
		t.Run(kind, func(t *testing.T) {
			var out bytes.Buffer
			w := zip.NewWriter(&out)
			entries := append([]File{{Path: ManifestName, Mode: 0644}, {Path: SignatureName, Mode: 0644}}, b.Manifest.Files...)
			if kind == "reordered" {
				entries[0], entries[1] = entries[1], entries[0]
			}
			if kind == "missing" {
				entries = entries[:len(entries)-1]
			}
			if kind == "duplicate" || kind == "excess" {
				entries = append(entries, entries[len(entries)-1])
			}
			for i, f := range entries {
				v := b.Files[f.Path]
				if f.Path == ManifestName {
					v = b.Encoded
				}
				if f.Path == SignatureName {
					v = b.Signature
				}
				h := &zip.FileHeader{Name: f.Path, Method: zip.Store}
				h.SetMode(os.FileMode(f.Mode))
				if i == 2 {
					switch kind {
					case "traversal":
						h.Name = "../escape"
					case "link":
						h.SetMode(os.ModeSymlink | 0755)
					case "mode":
						h.SetMode(os.ModeSetuid | 0755)
					case "oversize":
						v = append(append([]byte{}, v...), 1)
					}
				}
				if kind == "excess" && i == len(entries)-1 {
					h.Name = "extra"
				}
				if (kind == "manifest" && i == 0) || (kind == "signature" && i == 1) {
					v = append([]byte{}, v...)
					v[0] ^= 1
				}
				dst, err := w.CreateHeader(h)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = dst.Write(v); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err := Extract(out.Bytes(), "zip", key); err == nil {
				t.Fatal("accepted malicious archive")
			}
		})
	}
}

func TestRejectMaliciousTar(t *testing.T) {
	b, key := fixture(t)
	for _, kind := range []string{"traversal", "hardlink", "symlink", "duplicate", "mode", "oversize", "truncated", "extra-stream"} {
		t.Run(kind, func(t *testing.T) {
			var out bytes.Buffer
			gz := gzip.NewWriter(&out)
			w := tar.NewWriter(gz)
			entries := append([]File{{Path: ManifestName, Mode: 0644}, {Path: SignatureName, Mode: 0644}}, b.Manifest.Files...)
			if kind == "duplicate" {
				entries = append(entries, entries[len(entries)-1])
			}
			if kind == "truncated" {
				entries = entries[:len(entries)-1]
			}
			for i, f := range entries {
				v := b.Files[f.Path]
				if f.Path == ManifestName {
					v = b.Encoded
				}
				if f.Path == SignatureName {
					v = b.Signature
				}
				h := &tar.Header{Name: f.Path, Mode: int64(f.Mode), Size: int64(len(v)), Typeflag: tar.TypeReg}
				if i == 2 {
					switch kind {
					case "traversal":
						h.Name = "../outside"
					case "hardlink":
						h.Typeflag = tar.TypeLink
						h.Linkname = ManifestName
						h.Size = 0
						v = nil
					case "symlink":
						h.Typeflag = tar.TypeSymlink
						h.Linkname = ManifestName
						h.Size = 0
						v = nil
					case "mode":
						h.Mode = 04755
					case "oversize":
						v = append(append([]byte{}, v...), 1)
						h.Size++
					}
				}
				if err := w.WriteHeader(h); err != nil {
					t.Fatal(err)
				}
				if _, err := w.Write(v); err != nil {
					t.Fatal(err)
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if err := gz.Close(); err != nil {
				t.Fatal(err)
			}
			if kind == "extra-stream" {
				more := gzip.NewWriter(&out)
				if _, err := more.Write([]byte("unlisted")); err != nil {
					t.Fatal(err)
				}
				if err := more.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Extract(out.Bytes(), "tar.gz", key); err == nil {
				t.Fatal("accepted malicious tar")
			}
		})
	}
}

func TestReadOnlyTreeAndExecutableModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix permission bits; native Windows payload qualification tests read-only files")
	}
	b, key := fixture(t)
	root, _ := filepath.EvalSymlinks(t.TempDir())
	if err := WriteTree(root, b, key); err != nil {
		t.Fatal(err)
	}
	for _, f := range b.Manifest.Files {
		mode := os.FileMode(f.Mode) &^ 0222
		if err := os.Chmod(filepath.Join(root, filepath.FromSlash(f.Path)), mode); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := VerifyTree(root, Digest(b.Encoded), key); err != nil {
		t.Fatal("read-only payload rejected", err)
	}
	if err := os.Chmod(filepath.Join(root, filepath.FromSlash(b.Manifest.Entries["host"])), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyTree(root, Digest(b.Encoded), key); err == nil {
		t.Fatal("accepted nonexecutable native host")
	}
}
