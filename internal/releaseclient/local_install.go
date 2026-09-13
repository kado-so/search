package releaseclient

import (
	"context"
	"net/url"
	"path"
	"path/filepath"

	"github.com/kado-so/search/internal/buildinfo"
)

type localReleaseFetcher struct{ directory string }

func (f localReleaseFetcher) Fetch(_ context.Context, raw string, limit int64) ([]byte, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" {
		return nil, ErrInvalidMetadata
	}
	return readLocalFile(f.directory, path.Base(u.Path), limit)
}

// InstallLocalBundle consumes an already downloaded, signed release. The
// bootstrap executable verifies its own identity before publishing any files.
func InstallLocalBundle(ctx context.Context, directory, destination string, info buildinfo.Info) (Result, error) {
	if info.InstallChannel != "direct" || info.MCP == nil {
		return Result{}, ErrInstall
	}
	directory, err := filepath.Abs(directory)
	if err != nil {
		return Result{}, err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return Result{}, err
	}
	if _, _, err := VerifyLocalBundle(directory, info); err != nil {
		return Result{}, err
	}
	manager := Manager{PublicKey: info.ReleasePublicKey, MetadataURL: info.ReleaseMetadataURL, Fetcher: localReleaseFetcher{directory}}
	return manager.Install(ctx, Options{TargetPath: destination})
}
