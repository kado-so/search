package main

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/kado-so/search/internal/releaseclient"
)

type buildTarget struct {
	goos   string
	goarch string
}

var releaseTargets = []buildTarget{
	{goos: "darwin", goarch: "amd64"},
	{goos: "darwin", goarch: "arm64"},
	{goos: "linux", goarch: "amd64"},
	{goos: "linux", goarch: "arm64"},
	{goos: "windows", goarch: "amd64"},
	{goos: "windows", goarch: "arm64"},
}

type buildInput struct {
	root       string
	output     string
	goBinary   string
	source     releaseConfig
	commit     string
	builtAt    time.Time
	privateKey ed25519.PrivateKey
	publicKey  ed25519.PublicKey
	publicPEM  []byte
	keyID      string
}

type builtFile struct {
	file releaseclient.File
	data []byte
}

func buildRelease(input buildInput) error {
	assetBase := strings.TrimSuffix(input.source.InstallURL, "/") +
		"/releases/" + input.source.Version
	metadataURL := strings.TrimSuffix(input.source.InstallURL, "/") +
		"/releases/stable/release-metadata.json"
	license, err := os.ReadFile(filepath.Join(input.root, "LICENSE"))
	if err != nil {
		return errors.New("release license is unavailable")
	}
	modules, err := loadModules(input)
	if err != nil {
		return err
	}
	guide := []byte(installGuide(input.source, input.keyID))
	installUnix := []byte(installUnixScript(input.source, input.keyID))
	installPower := []byte(installPowerShellScript(input.source, input.keyID))
	uninstallUnix := []byte(uninstallUnixScript())
	uninstallPower := []byte(uninstallPowerShellScript())

	files := make(map[string]builtFile)
	add := func(name string, value []byte, mode fs.FileMode) (releaseclient.File, error) {
		if _, exists := files[name]; exists {
			return releaseclient.File{}, errors.New("release artifact name is duplicated")
		}
		if err := os.WriteFile(filepath.Join(input.output, name), value, mode); err != nil {
			return releaseclient.File{}, errors.New("release artifact could not be written")
		}
		descriptor := releaseclient.File{
			Name:   name,
			URL:    assetBase + "/" + name,
			SHA256: releaseclient.Digest(value),
			Size:   int64(len(value)),
		}
		files[name] = builtFile{file: descriptor, data: value}
		return descriptor, nil
	}
	register := func(
		name string,
		value []byte,
		descriptor releaseclient.File,
	) error {
		if _, exists := files[name]; exists {
			return errors.New("release artifact name is duplicated")
		}
		files[name] = builtFile{file: descriptor, data: value}
		return nil
	}
	if err := os.WriteFile(
		filepath.Join(input.output, "release-public-key.pem"),
		input.publicPEM,
		0o644,
	); err != nil {
		return errors.New("release public key could not be written")
	}
	guideFile, err := add("INSTALL-CLI.md", guide, 0o644)
	if err != nil {
		return err
	}
	installUnixFile, err := add("install.sh", installUnix, 0o755)
	if err != nil {
		return err
	}
	installPowerFile, err := add("install.ps1", installPower, 0o644)
	if err != nil {
		return err
	}
	uninstallUnixFile, err := add("uninstall.sh", uninstallUnix, 0o755)
	if err != nil {
		return err
	}
	uninstallPowerFile, err := add("uninstall.ps1", uninstallPower, 0o644)
	if err != nil {
		return err
	}

	targets := make([]releaseclient.Target, 0, len(releaseTargets))
	for _, target := range releaseTargets {
		built, err := buildTargetArtifacts(
			input,
			target,
			assetBase,
			metadataURL,
			license,
			guide,
			modules,
			add,
			register,
		)
		if err != nil {
			return err
		}
		targets = append(targets, built)
	}
	targets = releaseclient.SortedTargets(targets)

	provenance := makeProvenance(input, files)
	provenanceFile, err := add("provenance.intoto.json", provenance, 0o644)
	if err != nil {
		return err
	}
	checksums := makeChecksums(files)
	checksumsFile, err := add("checksums.txt", checksums, 0o644)
	if err != nil {
		return err
	}
	metadata := releaseclient.Metadata{
		SchemaVersion:    releaseclient.SchemaVersion,
		Product:          releaseclient.Product,
		Version:          input.source.Version,
		Commit:           input.commit,
		BuiltAt:          input.builtAt.Format(time.RFC3339),
		Repository:       input.source.Repository,
		InstallURL:       input.source.InstallURL,
		SigningAlgorithm: "Ed25519",
		KeyID:            input.keyID,
		SigningPublicKey: releaseclient.PublicKeyText(input.publicKey),
		Targets:          targets,
		Checksums:        checksumsFile,
		Provenance:       provenanceFile,
		InstallGuide:     guideFile,
		InstallUnix:      installUnixFile,
		InstallPower:     installPowerFile,
		UninstallUnix:    uninstallUnixFile,
		UninstallPower:   uninstallPowerFile,
	}
	metadataBytes, err := releaseclient.CanonicalMetadata(metadata)
	if err != nil {
		return errors.New("release metadata could not be encoded")
	}
	if err := metadata.Validate(); err != nil {
		return errors.New("release metadata did not pass validation")
	}
	signature := ed25519.Sign(input.privateKey, metadataBytes)
	if err := os.WriteFile(
		filepath.Join(input.output, "release-metadata.json"),
		metadataBytes,
		0o644,
	); err != nil {
		return errors.New("release metadata could not be written")
	}
	if err := os.WriteFile(
		filepath.Join(input.output, "release-metadata.json.sig"),
		signature,
		0o644,
	); err != nil {
		return errors.New("release metadata signature could not be written")
	}
	if _, err := releaseclient.VerifyMetadata(
		metadataBytes,
		signature,
		releaseclient.PublicKeyText(input.publicKey),
	); err != nil {
		return errors.New("release metadata signature self-check failed")
	}
	for _, target := range targets {
		if _, err := releaseclient.VerifyTargetArtifacts(
			metadata,
			target,
			provenance,
			files[target.SBOM.Name].data,
			files[target.Archive.Name].data,
		); err != nil {
			return fmt.Errorf(
				"release supply-chain self-check failed for %s/%s",
				target.OS,
				target.Arch,
			)
		}
	}
	return nil
}

func buildTargetArtifacts(
	input buildInput,
	target buildTarget,
	assetBase string,
	metadataURL string,
	license []byte,
	guide []byte,
	modules []module,
	add func(string, []byte, fs.FileMode) (releaseclient.File, error),
	register func(string, []byte, releaseclient.File) error,
) (releaseclient.Target, error) {
	suffix := ""
	binaryName := "kado"
	archiveFormat := "tar.gz"
	if target.goos == "windows" {
		suffix = ".exe"
		binaryName = "kado.exe"
		archiveFormat = "zip"
	}
	base := fmt.Sprintf(
		"kado_%s_%s_%s",
		input.source.Version,
		target.goos,
		target.goarch,
	)
	versionedBinaryName := base + suffix
	binaryPath := filepath.Join(input.output, versionedBinaryName)
	ldflags := strings.Join([]string{
		"-s",
		"-w",
		"-buildid=",
		"-X", "github.com/kado-so/search/internal/buildinfo.Version=" + input.source.Version,
		"-X", "github.com/kado-so/search/internal/buildinfo.Commit=" + input.commit,
		"-X", "github.com/kado-so/search/internal/buildinfo.Date=" + input.builtAt.Format(time.RFC3339),
		"-X", "github.com/kado-so/search/internal/buildinfo.Target=" + target.goos + "/" + target.goarch,
		"-X", "github.com/kado-so/search/internal/buildinfo.ReleasePublicKey=" + releaseclient.PublicKeyText(input.publicKey),
		"-X", "github.com/kado-so/search/internal/buildinfo.ReleaseKeyID=" + input.keyID,
		"-X", "github.com/kado-so/search/internal/buildinfo.ReleaseMetadataURL=" + metadataURL,
	}, " ")
	environment := append(sanitizedEnvironment(),
		"CGO_ENABLED=0",
		"GOOS="+target.goos,
		"GOARCH="+target.goarch,
		"SOURCE_DATE_EPOCH="+epochText(input.builtAt),
	)
	if _, err := commandOutput(
		input.root,
		environment,
		input.goBinary,
		"build",
		"-trimpath",
		"-buildvcs=false",
		"-ldflags",
		ldflags,
		"-o",
		binaryPath,
		"./cmd/kado",
	); err != nil {
		return releaseclient.Target{}, fmt.Errorf(
			"release build failed for %s/%s",
			target.goos,
			target.goarch,
		)
	}
	binary, err := os.ReadFile(binaryPath)
	if err != nil {
		return releaseclient.Target{}, errors.New("release binary could not be read")
	}
	binaryFile := releaseclient.File{
		Name:   versionedBinaryName,
		URL:    assetBase + "/" + versionedBinaryName,
		SHA256: releaseclient.Digest(binary),
		Size:   int64(len(binary)),
	}
	// The binary was written by go build, so register it without rewriting it.
	filesMode := fs.FileMode(0o755)
	if target.goos == "windows" {
		filesMode = 0o644
	}
	if err := os.Chmod(binaryPath, filesMode); err != nil {
		return releaseclient.Target{}, errors.New("release binary mode could not be set")
	}

	sbomName := base + ".spdx.json"
	sbom := makeSBOM(input, target, sbomName, modules)
	sbomFile, err := add(sbomName, sbom, 0o644)
	if err != nil {
		return releaseclient.Target{}, err
	}
	archiveName := base + ".tar.gz"
	var archive []byte
	if archiveFormat == "zip" {
		archiveName = base + ".zip"
		archive, err = makeZip(input.builtAt, binaryName, binary, license, guide)
	} else {
		archive, err = makeTarGzip(input.builtAt, binaryName, binary, license, guide)
	}
	if err != nil {
		return releaseclient.Target{}, err
	}
	archiveFile, err := add(archiveName, archive, 0o644)
	if err != nil {
		return releaseclient.Target{}, err
	}
	// Register the existing direct binary after add's duplicate checks.
	if _, err := os.Stat(binaryPath); err != nil {
		return releaseclient.Target{}, errors.New("release binary disappeared")
	}
	if err := register(versionedBinaryName, binary, binaryFile); err != nil {
		return releaseclient.Target{}, err
	}
	return releaseclient.Target{
		OS:            target.goos,
		Arch:          target.goarch,
		BinaryName:    binaryName,
		ArchiveFormat: archiveFormat,
		Binary:        binaryFile,
		Archive:       archiveFile,
		SBOM:          sbomFile,
	}, nil
}

func makeTarGzip(
	builtAt time.Time,
	binaryName string,
	binary []byte,
	license []byte,
	guide []byte,
) ([]byte, error) {
	var output bytes.Buffer
	compressed, err := gzip.NewWriterLevel(&output, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	compressed.Header.ModTime = builtAt
	compressed.Header.OS = 255
	archive := tar.NewWriter(compressed)
	for _, entry := range []struct {
		name string
		mode int64
		data []byte
	}{
		{name: binaryName, mode: 0o755, data: binary},
		{name: "LICENSE", mode: 0o644, data: license},
		{name: "INSTALL-CLI.md", mode: 0o644, data: guide},
	} {
		header := &tar.Header{
			Name:       entry.name,
			Mode:       entry.mode,
			Size:       int64(len(entry.data)),
			ModTime:    builtAt,
			AccessTime: time.Time{},
			ChangeTime: time.Time{},
			Uid:        0,
			Gid:        0,
			Uname:      "",
			Gname:      "",
			Typeflag:   tar.TypeReg,
			Format:     tar.FormatUSTAR,
		}
		if err := archive.WriteHeader(header); err != nil {
			return nil, err
		}
		if _, err := archive.Write(entry.data); err != nil {
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

func makeZip(
	builtAt time.Time,
	binaryName string,
	binary []byte,
	license []byte,
	guide []byte,
) ([]byte, error) {
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	for _, entry := range []struct {
		name string
		mode fs.FileMode
		data []byte
	}{
		{name: binaryName, mode: 0o755, data: binary},
		{name: "LICENSE", mode: 0o644, data: license},
		{name: "INSTALL-CLI.md", mode: 0o644, data: guide},
	} {
		header := &zip.FileHeader{Name: entry.name, Method: zip.Store}
		header.SetModTime(builtAt)
		header.SetMode(entry.mode)
		writer, err := archive.CreateHeader(header)
		if err != nil {
			return nil, err
		}
		if _, err := writer.Write(entry.data); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func makeChecksums(files map[string]builtFile) []byte {
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	var output strings.Builder
	for _, name := range names {
		fmt.Fprintf(&output, "%s  %s\n", files[name].file.SHA256, name)
	}
	return []byte(output.String())
}
