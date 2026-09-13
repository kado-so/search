package launcher

import (
	"crypto/ed25519"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kado-so/search/internal/payload"
)

// UninstallComplete stops only authenticated owned bridges across all homes,
// then refuses removal if any reference remains. It never visits credentials.
// A locked executable or incomplete filesystem cleanup is an explicit busy
// failure; callers must not report success or purge credentials after it.
func UninstallComplete(launcher string, key ed25519.PublicKey) error {
	if !directInstallation(launcher) {
		return errors.New("installation is package managed")
	}
	return WithUpdateLock(launcher, func() error {
		active, err := ActiveComplete(launcher, key)
		if err != nil {
			return ErrBusy
		}
		encoded, err := payload.ReadFile(active.Root, payload.ManifestName, payload.MaxManifest)
		if err != nil {
			return err
		}
		signature, err := payload.ReadFile(active.Root, payload.SignatureName, ed25519.SignatureSize)
		if err != nil {
			return err
		}
		b := payload.Bundle{Manifest: active.Manifest, Encoded: encoded, Signature: signature, Files: map[string][]byte{}}
		for _, f := range active.Manifest.Files {
			v, err := payload.ReadFile(active.Root, f.Path, f.Size)
			if err != nil {
				return err
			}
			b.Files[f.Path] = v
		}
		// Keep the trusted maintenance runtime outside the tree being removed.
		temporary, err := os.MkdirTemp(filepath.Dir(launcher), ".kado-uninstall-")
		if err != nil {
			return err
		}
		defer os.RemoveAll(temporary)
		if err := payload.WriteTree(temporary, b, key); err != nil {
			return err
		}
		runner := CompletePaths{Root: temporary, Manifest: active.Manifest, Digest: active.Digest}
		root := managedRoot(launcher)
		if err := withPayloadMaintenance(root, runner, key, true, func(refs map[string]bool, alive func() error) error {
			if len(refs) != 0 {
				return ErrBusy
			}
			// Keep the format marker until removal succeeds, so a busy/failed
			// uninstall can never fall through to a legacy executable bootstrap.
			for _, name := range []string{"versions"} {
				p := filepath.Join(root, name)
				if err := payload.PlainPath(p); err != nil {
					return err
				}
				if err := removePayloadTree(p, alive); err != nil {
					return ErrBusy
				}
			}
			if err := alive(); err != nil {
				return err
			}
			if err := os.Remove(launcher); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return ErrBusy
			}
			return nil
		}); err != nil {
			return err
		}
		// All executable payloads are gone before the home gates are released.
		// The inventory and lock sentinels can now be removed without permitting
		// a new bridge to start from this installation.
		entries, err := os.ReadDir(root)
		if err != nil {
			return err
		}
		for _, e := range entries {
			if e.Name() == ".update.lock" {
				continue
			}
			p := filepath.Join(root, e.Name())
			if err := payload.PlainPath(p); err != nil {
				return err
			}
			if err := os.RemoveAll(p); err != nil {
				return ErrBusy
			}
		}
		if err := os.Remove(filepath.Join(filepath.Dir(launcher), receiptName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return syncDirectory(filepath.Dir(launcher))
	})
}
