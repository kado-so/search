package releaseclient

import (
	"context"
	"crypto/ed25519"
	"errors"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/kado-so/search/internal/launcher"
	"github.com/kado-so/search/internal/payload"
)

func VerifyCompleteTarget(target Target, archive []byte, key ed25519.PublicKey) (payload.Bundle, error) {
	if target.Payload == nil || VerifyFile(target.Archive, archive) != nil {
		return payload.Bundle{}, ErrChecksum
	}
	_, format, ok := targetLayout(target.OS)
	if !ok {
		return payload.Bundle{}, ErrPlatform
	}
	b, err := payload.Extract(archive, format, key)
	if err != nil || b.Manifest.Target != target.OS+"/"+target.Arch || target.Payload.Size != int64(len(b.Encoded)) || target.Payload.SHA256 != Digest(b.Encoded) || b.Manifest.MCP.NodeArchiveSHA256 != target.NodeArchiveSHA256 {
		return payload.Bundle{}, ErrChecksum
	}
	a2a := b.Files[b.Manifest.Entries["a2a"]]
	if target.Sidecar.Size != int64(len(a2a)) || target.Sidecar.SHA256 != Digest(a2a) {
		return payload.Bundle{}, ErrChecksum
	}
	return b, nil
}

func (manager Manager) updateComplete(ctx context.Context, options Options, result Result, metadata Metadata, target Target, archive []byte) (Result, error) {
	key, err := ParsePublicKey(manager.PublicKey)
	if err != nil {
		return result, ErrInvalidSignature
	}
	b, err := VerifyCompleteTarget(target, archive, key)
	if err != nil {
		return result, err
	}
	c := b.Manifest.MCP
	c.NodeArchiveSHA256 = ""
	if metadata.Components.MCP == nil || c != *metadata.Components.MCP || b.Manifest.Version != metadata.Version {
		return result, ErrCandidate
	}
	if options.LauncherPath == "" || options.TargetPath != options.LauncherPath {
		return result, ErrInstall
	}
	candidate, cleanup, err := writeCandidate(options.TargetPath, b.Files[b.Manifest.Entries["kado"]])
	if err != nil {
		return result, ErrInstall
	}
	defer cleanup()
	verifier := manager.VerifyCandidate
	if verifier == nil {
		verifier = VerifyExecutable
	}
	if err := verifier(ctx, candidate, metadata, target); err != nil {
		return result, ErrCandidate
	}
	if options.DryRun {
		return result, nil
	}
	before, beforeErr := launcher.ActiveComplete(options.LauncherPath, key)
	installErr := launcher.InstallCompleteLocked(options.LauncherPath, b, key)
	after, afterErr := launcher.ActiveComplete(options.LauncherPath, key)
	result.Changed = afterErr == nil && (beforeErr != nil || before.Digest != after.Digest)
	return result, installErr
}

// Install performs a fresh direct installation of the successor format. The
// stable launcher is published last. Existing executable installations are
// handled by Update; activation-v2 is never migrated through this entry point.
func (manager Manager) Install(ctx context.Context, options Options) (Result, error) {
	if options.TargetPath == "" || options.CurrentVersion != "" {
		return Result{}, ErrInstall
	}
	options.LauncherPath = options.TargetPath
	options.fresh = true
	if err := validateTargetPath(options.TargetPath, false); err != nil {
		return Result{}, ErrInstall
	}
	if err := payload.PlainPath(filepath.Dir(options.TargetPath)); err != nil {
		return Result{}, ErrInstall
	}
	var result Result
	err := launcher.WithUpdateLock(options.TargetPath, func() error {
		if _, err := os.Lstat(options.TargetPath); !errors.Is(err, fs.ErrNotExist) {
			return ErrInstall
		}
		var err error
		result, err = manager.update(ctx, options)
		if err != nil || options.DryRun {
			return err
		}
		key, err := ParsePublicKey(manager.PublicKey)
		if err != nil {
			return err
		}
		active, err := launcher.ActiveComplete(options.TargetPath, key)
		if err != nil {
			return ErrInstall
		}
		value, err := payload.ReadFile(active.Root, active.Manifest.Entries["kado"], payload.MaxFile)
		if err != nil {
			return err
		}
		candidate, cleanup, err := writeCandidate(options.TargetPath, value)
		if err != nil {
			return err
		}
		defer cleanup()
		if err := os.Rename(candidate, options.TargetPath); err != nil {
			return err
		}
		return syncDirectory(filepath.Dir(options.TargetPath))
	})
	return result, err
}
