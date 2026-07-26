package releaseclient

import (
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/kado-so/search/internal/buildinfo"
)

// VerifyLocalBundle verifies a downloaded release directory using the public
// key stamped into the candidate executable. It performs no writes.
func VerifyLocalBundle(
	directory string,
	info buildinfo.Info,
) (Metadata, Target, error) {
	absolute, err := filepath.Abs(directory)
	if err != nil || filepath.Clean(absolute) != absolute {
		return Metadata{}, Target{}, ErrInvalidMetadata
	}
	directoryInfo, err := os.Lstat(absolute)
	if err != nil || !directoryInfo.IsDir() ||
		directoryInfo.Mode()&fs.ModeSymlink != 0 {
		return Metadata{}, Target{}, ErrInvalidMetadata
	}
	metadataBytes, err := readLocalFile(
		absolute,
		"release-metadata.json",
		MaxMetadataSize,
	)
	if err != nil {
		return Metadata{}, Target{}, err
	}
	signature, err := readLocalFile(
		absolute,
		"release-metadata.json.sig",
		ed25519SignatureSize,
	)
	if err != nil {
		return Metadata{}, Target{}, err
	}
	metadata, err := VerifyMetadata(
		metadataBytes,
		signature,
		info.ReleasePublicKey,
	)
	if err != nil {
		if errors.Is(err, errInvalidSignature) {
			return Metadata{}, Target{}, ErrInvalidSignature
		}
		return Metadata{}, Target{}, ErrInvalidMetadata
	}
	target, err := metadata.TargetFor(runtime.GOOS, runtime.GOARCH)
	if err != nil {
		return Metadata{}, Target{}, ErrPlatform
	}
	if info.Version != metadata.Version ||
		info.Commit != metadata.Commit ||
		info.Date != metadata.BuiltAt ||
		info.Target != target.OS+"/"+target.Arch ||
		info.ReleaseKeyID != metadata.KeyID {
		return Metadata{}, Target{}, ErrCandidate
	}
	archive, err := readAndVerifyLocal(absolute, target.Archive, MaxArchiveSize)
	if err != nil {
		return Metadata{}, Target{}, err
	}
	binary, err := VerifyTargetArchive(target, archive)
	if err != nil {
		return Metadata{}, Target{}, err
	}
	executable, err := os.Executable()
	if err != nil {
		return Metadata{}, Target{}, ErrCandidate
	}
	running, err := os.ReadFile(executable)
	if err != nil || !bytes.Equal(running, binary) {
		return Metadata{}, Target{}, ErrCandidate
	}
	return metadata, target, nil
}

func readAndVerifyLocal(
	directory string,
	file File,
	limit int64,
) ([]byte, error) {
	value, err := readLocalFile(directory, file.Name, limit)
	if err != nil {
		return nil, err
	}
	if err := VerifyFile(file, value); err != nil {
		return nil, ErrChecksum
	}
	return value, nil
}

func readLocalFile(directory, name string, limit int64) ([]byte, error) {
	if !safeName.MatchString(name) {
		return nil, ErrInvalidMetadata
	}
	path := filepath.Join(directory, name)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() ||
		info.Mode()&fs.ModeSymlink != 0 ||
		info.Size() <= 0 ||
		info.Size() > limit {
		return nil, ErrInvalidMetadata
	}
	value, err := os.ReadFile(path)
	if err != nil || int64(len(value)) != info.Size() {
		return nil, ErrInvalidMetadata
	}
	return value, nil
}
