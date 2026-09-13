package main

import (
	"archive/tar"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func TestComponentExtractionRejectsUnsafeMembers(t *testing.T) {
	for _, test := range []struct {
		name    string
		headers []tar.Header
		ok      bool
	}{
		{"regular", []tar.Header{{Name: "app/dist/cli/index.js", Typeflag: tar.TypeReg, Mode: 0644, Size: 1}}, true},
		{"traversal", []tar.Header{{Name: "../outside", Typeflag: tar.TypeReg, Mode: 0644}}, false},
		{"link", []tar.Header{{Name: "app/link", Typeflag: tar.TypeSymlink, Linkname: "../../outside", Mode: 0644}}, false},
		{"case collision", []tar.Header{{Name: "app/A", Typeflag: tar.TypeReg, Mode: 0644}, {Name: "app/a", Typeflag: tar.TypeReg, Mode: 0644}}, false},
		{"device path", []tar.Header{{Name: "app/NUL", Typeflag: tar.TypeReg, Mode: 0644}}, false},
		{"file parent", []tar.Header{{Name: "app", Typeflag: tar.TypeReg, Mode: 0644}, {Name: "app/file", Typeflag: tar.TypeReg, Mode: 0644}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			archive := filepath.Join(root, "input.tar.gz")
			f, err := os.Create(archive)
			if err != nil {
				t.Fatal(err)
			}
			z := gzip.NewWriter(f)
			w := tar.NewWriter(z)
			for _, h := range test.headers {
				if err := w.WriteHeader(&h); err != nil {
					t.Fatal(err)
				}
				if h.Size > 0 {
					if _, err := w.Write([]byte("x")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := w.Close(); err != nil {
				t.Fatal(err)
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			if err := f.Close(); err != nil {
				t.Fatal(err)
			}
			err = unpack(archive, filepath.Join(root, "output"))
			if (err == nil) != test.ok {
				t.Fatalf("accepted=%v error=%v", err == nil, err)
			}
		})
	}
}
