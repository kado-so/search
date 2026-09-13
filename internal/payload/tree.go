package payload

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
)

// PlainPath rejects redirected ancestors, including Windows junctions.
func PlainPath(p string) error {
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		return ErrInvalid
	}
	for {
		if err := plain(p); err != nil {
			return err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return nil
		}
		p = parent
	}
}

func ReadFile(root, name string, limit int64) ([]byte, error) {
	if !ValidPath(name) {
		return nil, ErrInvalid
	}
	p := filepath.Join(root, filepath.FromSlash(name))
	if err := PlainPath(p); err != nil {
		return nil, err
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	s, err := f.Stat()
	if err != nil || !s.Mode().IsRegular() || s.Size() < 0 || s.Size() > limit {
		return nil, ErrInvalid
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) != s.Size() {
		return nil, ErrInvalid
	}
	return b, nil
}

// VerifyTree authenticates every byte and rejects unlisted files/directories.
// expectedDigest is from the activation, whose version/target callers also bind.
func VerifyTree(root, expectedDigest string, key ed25519.PublicKey) (Manifest, error) {
	var m Manifest
	if !ValidDigest(expectedDigest) {
		return m, ErrInvalid
	}
	encoded, err := ReadFile(root, ManifestName, MaxManifest)
	if err != nil || Digest(encoded) != expectedDigest {
		return m, ErrInvalid
	}
	sig, err := ReadFile(root, SignatureName, ed25519.SignatureSize)
	if err != nil {
		return m, err
	}
	m, err = Authenticate(encoded, sig, key)
	if err != nil {
		return m, err
	}
	wanted := map[string]File{}
	dirs := map[string]bool{".": true}
	for _, f := range m.Files {
		wanted[f.Path] = f
		for d := filepath.Dir(filepath.FromSlash(f.Path)); d != "."; d = filepath.Dir(d) {
			dirs[filepath.ToSlash(d)] = true
		}
	}
	seen := 0
	err = filepath.WalkDir(root, func(p string, e fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := plain(p); err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if e.IsDir() {
			if !dirs[rel] {
				return ErrInvalid
			}
			return nil
		}
		if rel == ManifestName || rel == SignatureName {
			return nil
		}
		f, ok := wanted[rel]
		if !ok {
			return ErrInvalid
		}
		s, err := e.Info()
		if err != nil || !s.Mode().IsRegular() || s.Size() != f.Size {
			return ErrInvalid
		}
		// Read-only installation is valid; executable bits must survive extraction.
		if runtime.GOOS != "windows" && (s.Mode().Perm()&0111 != fs.FileMode(f.Mode)&0111 || s.Mode()&(fs.ModeSetuid|fs.ModeSetgid|fs.ModeSticky) != 0) {
			return ErrInvalid
		}
		h, err := os.Open(p)
		if err != nil {
			return err
		}
		d := sha256.New()
		n, err := io.Copy(d, io.LimitReader(h, f.Size+1))
		closeErr := h.Close()
		if err != nil || closeErr != nil || n != f.Size || hex.EncodeToString(d.Sum(nil)) != f.SHA256 {
			return ErrInvalid
		}
		seen++
		return nil
	})
	if err == nil && seen != len(wanted) {
		err = ErrInvalid
	}
	return m, err
}

// WriteTree only accepts a new empty staging directory. Publication belongs to
// the installer; no existing version is modified here.
func WriteTree(root string, b Bundle, key ed25519.PublicKey) error {
	m, err := Authenticate(b.Encoded, b.Signature, key)
	if err != nil {
		return err
	}
	if err := PlainPath(root); err != nil {
		return err
	}
	existing, err := os.ReadDir(root)
	if err != nil || len(existing) != 0 || len(b.Files) != len(m.Files) {
		return ErrInvalid
	}
	write := func(name string, value []byte, mode fs.FileMode) error {
		p := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			return err
		}
		if err := PlainPath(filepath.Dir(p)); err != nil {
			return err
		}
		f, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if err != nil {
			return err
		}
		if err = f.Chmod(mode); err == nil {
			_, err = f.Write(value)
		}
		if err == nil {
			err = f.Sync()
		}
		closeErr := f.Close()
		if err != nil {
			return err
		}
		return closeErr
	}
	if err := write(ManifestName, b.Encoded, 0644); err != nil {
		return err
	}
	if err := write(SignatureName, b.Signature, 0644); err != nil {
		return err
	}
	for _, f := range m.Files {
		v, ok := b.Files[f.Path]
		if !ok || int64(len(v)) != f.Size || Digest(v) != f.SHA256 {
			return ErrInvalid
		}
		if err := write(f.Path, v, fs.FileMode(f.Mode)); err != nil {
			return err
		}
	}
	_, err = VerifyTree(root, Digest(b.Encoded), key)
	return err
}
