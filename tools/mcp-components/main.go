// Command mcp-components safely assembles six native runner artifacts for the
// complete release builder. It never runs code from the component archives.
package main

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kado-so/search/internal/payload"
)

func main() {
	input := flag.String("input", "", "directory of runner archives and separate manifest digests")
	output := flag.String("out", "", "new component directory")
	flag.Parse()
	if err := assemble(*input, *output); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func assemble(input, output string) error {
	if input == "" || output == "" {
		return errors.New("--input and --out are required")
	}
	var err error
	output, err = filepath.Abs(output)
	if err != nil {
		return err
	}
	if _, err := os.Lstat(output); !os.IsNotExist(err) {
		return errors.New("output must not exist")
	}
	if err := os.MkdirAll(output, 0700); err != nil {
		return err
	}
	index := map[string]string{}
	for _, target := range []string{"windows-amd64", "windows-arm64", "darwin-amd64", "darwin-arm64", "linux-amd64", "linux-arm64"} {
		encoded, err := os.ReadFile(filepath.Join(input, target+".manifest-sha256"))
		digest := strings.TrimSpace(string(encoded))
		if err != nil || !payload.ValidDigest(digest) {
			return errors.New("missing native manifest digest: " + target)
		}
		root := filepath.Join(output, target)
		if err := unpack(filepath.Join(input, target+".gen.tar.gz"), root); err != nil {
			return fmt.Errorf("%s: %w", target, err)
		}
		manifest, err := payload.ReadFile(root, "payload.gen.json", payload.MaxManifest)
		if err != nil || payload.Digest(manifest) != digest {
			return errors.New("native manifest digest mismatch: " + target)
		}
		index[strings.Replace(target, "-", "/", 1)] = digest
	}
	encoded, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(output, "digests.gen.json"), append(encoded, '\n'), 0600)
}

func unpack(archive, root string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() > payload.MaxArchive {
		return payload.ErrInvalid
	}
	z, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	defer z.Close()
	reader := tar.NewReader(io.LimitReader(z, payload.MaxExpanded+payload.MaxManifest*2))
	seen := map[string]bool{}
	var total int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		key := strings.ToLower(header.Name)
		if !payload.ValidPath(header.Name) || seen[key] || len(seen) >= payload.MaxFiles || header.Typeflag != tar.TypeReg || header.Size < 0 || header.Size > payload.MaxFile || (header.Mode != 0644 && header.Mode != 0755) {
			return payload.ErrInvalid
		}
		seen[key] = true
		total += header.Size
		if total > payload.MaxExpanded {
			return payload.ErrInvalid
		}
		destination := filepath.Join(root, filepath.FromSlash(header.Name))
		if err := os.MkdirAll(filepath.Dir(destination), 0700); err != nil {
			return err
		}
		// No pre-existing file, link, or reparse point can be overwritten.
		if err := payload.PlainPath(filepath.Dir(destination)); err != nil {
			return err
		}
		out, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(header.Mode))
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(out, reader, header.Size)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
	}
}
