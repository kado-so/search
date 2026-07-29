package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"errors"
	"sort"
	"time"

	"github.com/kado-so/search/internal/releaseclient"
	"github.com/kado-so/search/internal/skillclient"
	kadoskill "github.com/kado-so/search/skills/kado-search"
)

func makeSkillRelease(
	builtAt time.Time,
	baseURL string,
	cliVersion string,
	private ed25519.PrivateKey,
) (archive, metadata, signature []byte, err error) {
	files, err := kadoskill.Bundle()
	if err != nil {
		return nil, nil, nil, errors.New("bundled skill is unavailable")
	}
	version := kadoskill.Version()
	if version == "" {
		return nil, nil, nil, errors.New("bundled skill version is invalid")
	}
	archive, err = makeSkillArchive(builtAt, files)
	if err != nil {
		return nil, nil, nil, err
	}
	descriptor := skillclient.Metadata{
		SchemaVersion:     skillclient.SchemaVersion,
		Name:              skillclient.SkillName,
		Version:           version,
		MinimumCLIVersion: cliVersion,
		Archive: skillclient.Archive{
			URL:    baseURL + "/skills/kado-search/latest/kado-search.tar.gz",
			Size:   int64(len(archive)),
			SHA256: releaseclient.Digest(archive),
		},
	}
	metadata, err = skillclient.CanonicalMetadata(descriptor)
	if err != nil {
		return nil, nil, nil, errors.New("skill metadata could not be encoded")
	}
	signature = ed25519.Sign(private, metadata)
	if _, err := skillclient.VerifyMetadata(
		metadata,
		signature,
		releaseclient.PublicKeyText(private.Public().(ed25519.PublicKey)),
		baseURL+"/skills/kado-search/latest/metadata.json",
	); err != nil {
		return nil, nil, nil, errors.New("skill metadata self-check failed")
	}
	return archive, metadata, signature, nil
}

func makeSkillArchive(
	builtAt time.Time,
	files map[string][]byte,
) ([]byte, error) {
	var output bytes.Buffer
	compressed, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	compressed.Header.ModTime = builtAt
	compressed.Header.OS = 255
	archive := tar.NewWriter(compressed)
	for _, name := range sortedSkillNames(files) {
		value := files[name]
		header := &tar.Header{
			Name:       "kado-search/" + name,
			Mode:       0o644,
			Size:       int64(len(value)),
			ModTime:    builtAt,
			AccessTime: time.Time{},
			ChangeTime: time.Time{},
			Typeflag:   tar.TypeReg,
			Format:     tar.FormatUSTAR,
		}
		if err := archive.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := archive.Write(value); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	if err := compressed.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func sortedSkillNames(files map[string][]byte) []string {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
