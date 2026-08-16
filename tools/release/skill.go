package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/kado-so/search/internal/releaseclient"
	"github.com/kado-so/search/internal/skillclient"
)

type builtSkillRelease struct {
	Name, Variant, Version       string
	Archive, Metadata, Signature []byte
}

type builtSkillCatalog struct {
	Catalog, Signature []byte
	Releases           []builtSkillRelease
}

func makeSkillReleases(baseURL string, private ed25519.PrivateKey) (builtSkillCatalog, error) {
	var output builtSkillCatalog
	catalog, embedded, err := skillclient.EmbeddedCatalog()
	if err != nil {
		return builtSkillCatalog{}, err
	}
	for skillIndex := range catalog.Skills {
		skill := &catalog.Skills[skillIndex]
		for variantIndex := range skill.Variants {
			variant := &skill.Variants[variantIndex]
			bundle, ok := embedded[skill.Name+":"+variant.ID]
			if !ok {
				return builtSkillCatalog{}, fmt.Errorf("embedded skill %s/%s is unavailable", skill.Name, variant.ID)
			}
			archive, err := makeSkillArchive(bundle.Files, skill.Name)
			if err != nil {
				return builtSkillCatalog{}, err
			}
			metadataURL := fmt.Sprintf("%s/skills/%s/%s/%s/metadata.json", baseURL, skill.Name, variant.ID, bundle.Metadata.Version)
			bundle.Metadata.Archive = skillclient.Archive{URL: strings.TrimSuffix(metadataURL, "/metadata.json") + "/" + skill.Name + ".tar.gz", Size: int64(len(archive)), SHA256: releaseclient.Digest(archive)}
			metadata, err := skillclient.CanonicalMetadata(bundle.Metadata)
			if err != nil {
				return builtSkillCatalog{}, err
			}
			signature := ed25519.Sign(private, metadata)
			if _, err := skillclient.VerifyMetadata(metadata, signature, releaseclient.PublicKeyText(private.Public().(ed25519.PublicKey)), metadataURL, skill.Name, variant.ID); err != nil {
				return builtSkillCatalog{}, fmt.Errorf("skill metadata self-check failed: %w", err)
			}
			variant.MetadataURL = metadataURL
			output.Releases = append(output.Releases, builtSkillRelease{Name: skill.Name, Variant: variant.ID, Version: bundle.Metadata.Version, Archive: archive, Metadata: metadata, Signature: signature})
		}
	}
	catalogBytes, err := skillclient.CanonicalCatalog(catalog)
	if err != nil {
		return builtSkillCatalog{}, err
	}
	catalogSignature := ed25519.Sign(private, catalogBytes)
	if _, err := skillclient.VerifyCatalog(catalogBytes, catalogSignature, releaseclient.PublicKeyText(private.Public().(ed25519.PublicKey)), baseURL+"/skills/latest/catalog.json"); err != nil {
		return builtSkillCatalog{}, fmt.Errorf("skill catalog self-check failed: %w", err)
	}
	output.Catalog, output.Signature = catalogBytes, catalogSignature
	return output, nil
}

func makeSkillRelease(
	baseURL string,
	private ed25519.PrivateKey,
) (archive, metadata, signature []byte, err error) {
	set, err := makeSkillReleases(baseURL, private)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, release := range set.Releases {
		if release.Name == skillclient.SkillName && release.Variant == "default" {
			return release.Archive, release.Metadata, release.Signature, nil
		}
	}
	return nil, nil, nil, fmt.Errorf("bundled search skill is unavailable")
}

func makeSkillArchive(files map[string][]byte, names ...string) ([]byte, error) {
	skillName := skillclient.SkillName
	if len(names) > 0 {
		skillName = names[0]
	}
	// The skill is independently versioned from the CLI. Keep its archive
	// bytes stable when an unchanged skill is included in later CLI releases.
	archivedAt := time.Date(1980, time.January, 1, 0, 0, 0, 0, time.UTC)
	var output bytes.Buffer
	compressed, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	compressed.Header.ModTime = archivedAt
	compressed.Header.OS = 255
	archive := tar.NewWriter(compressed)
	for _, name := range sortedSkillNames(files) {
		value := files[name]
		header := &tar.Header{
			Name:       skillName + "/" + name,
			Mode:       0o644,
			Size:       int64(len(value)),
			ModTime:    archivedAt,
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
