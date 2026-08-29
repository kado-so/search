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
	"path"
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
	root         string
	output       string
	kadoPrebuilt string
	a2aPrebuilt  string
	goBinary     string
	a2a          a2aPreparedSource
	a2aLicense   []byte
	source       releaseIdentity
	commit       string
	builtAt      time.Time
	privateKey   ed25519.PrivateKey
	publicKey    ed25519.PublicKey
	publicPEM    []byte
	keyID        string
	channel      string
	targets      []buildTarget
}

type builtFile struct {
	file releaseclient.File
	data []byte
}

func buildRelease(input buildInput) error {
	assetBase := strings.TrimSuffix(input.source.InstallURL, "/") +
		"/releases/" + input.source.Version
	license, err := os.ReadFile(filepath.Join(input.root, "LICENSE"))
	if err != nil {
		return errors.New("release license is unavailable")
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
		if err := writeReleaseArtifact(input.output, name, value, mode); err != nil {
			return releaseclient.File{}, err
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
	_, err = add("INSTALL-CLI.md", guide, 0o644)
	if err != nil {
		return err
	}
	_, err = add("install.sh", installUnix, 0o755)
	if err != nil {
		return err
	}
	_, err = add("install.ps1", installPower, 0o644)
	if err != nil {
		return err
	}
	_, err = add("uninstall.sh", uninstallUnix, 0o755)
	if err != nil {
		return err
	}
	_, err = add("uninstall.ps1", uninstallPower, 0o644)
	if err != nil {
		return err
	}
	if err := addSkillReleaseArtifacts(input.source.InstallURL, input.privateKey, add); err != nil {
		return err
	}
	buildTargets := input.targets
	if len(buildTargets) == 0 {
		buildTargets = releaseTargets
	}
	targets := make([]releaseclient.Target, 0, len(buildTargets))
	for _, target := range buildTargets {
		built, err := buildTargetArtifacts(
			input,
			target,
			assetBase,
			license,
			guide,
			add,
			register,
		)
		if err != nil {
			return err
		}
		targets = append(targets, built)
	}
	targets = releaseclient.SortedTargets(targets)

	provenance, err := makeProvenance(input, files, targets)
	if err != nil {
		return err
	}
	provenanceFile, err := add("provenance.intoto.json", provenance, 0o644)
	if err != nil {
		return err
	}
	checksums := makeChecksums(files)
	_, err = add("checksums.txt", checksums, 0o644)
	if err != nil {
		return err
	}
	metadata := releaseclient.Metadata{
		SchemaVersion: releaseclient.SchemaVersion,
		Product:       releaseclient.Product,
		Version:       input.source.Version,
		Commit:        input.commit,
		BuiltAt:       input.builtAt.Format(time.RFC3339),
		KeyID:         input.keyID,
		Components: releaseclient.Components{A2ACLI: releaseclient.A2AComponent{
			Repository:          input.a2a.Lock.Repository,
			Module:              input.a2a.Lock.Module,
			Version:             input.a2a.Lock.Version,
			Tag:                 a2aTagValue(input.a2a.Lock.Tag),
			Commit:              input.a2a.Lock.Commit,
			SourceArchiveSHA256: input.a2a.Lock.SourceArchiveSHA256,
			SourceTreeSHA256:    input.a2a.Lock.SourceTreeSHA256,
			PatchedTreeSHA256:   input.a2a.Lock.PatchedTreeSHA256,
			GoModSHA256:         input.a2a.Lock.GoModSHA256,
			GoSumSHA256:         input.a2a.Lock.GoSumSHA256,
			LicenseSHA256:       input.a2a.Lock.License.SHA256,
			GoToolchain:         input.a2a.Lock.GoToolchain,
			DisplayName:         input.a2a.Lock.DisplayName,
			PatchSetSHA256:      input.a2a.PatchSetSHA256,
			BuiltAt:             input.builtAt.Format(time.RFC3339),
		}},
		Provenance: provenanceFile,
		Targets:    targets,
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
		if _, err := releaseclient.VerifyTargetBundle(
			target,
			files[target.Archive.Name].data,
		); err != nil {
			return fmt.Errorf(
				"release archive self-check failed for %s/%s",
				target.OS,
				target.Arch,
			)
		}
	}
	return nil
}

func addSkillReleaseArtifacts(
	baseURL string,
	privateKey ed25519.PrivateKey,
	add func(string, []byte, fs.FileMode) (releaseclient.File, error),
) error {
	skillSet, err := makeSkillReleases(baseURL, privateKey)
	if err != nil {
		return err
	}
	if _, err := add("skills/catalog.json", skillSet.Catalog, 0o644); err != nil {
		return err
	}
	if _, err := add("skills/catalog.json.sig", skillSet.Signature, 0o644); err != nil {
		return err
	}
	for _, skill := range skillSet.Releases {
		prefix := path.Join("skills", skill.Name, skill.Variant, skill.Version)
		if _, err := add(path.Join(prefix, skill.Name+".tar.gz"), skill.Archive, 0o644); err != nil {
			return err
		}
		if _, err := add(path.Join(prefix, "metadata.json"), skill.Metadata, 0o644); err != nil {
			return err
		}
		if _, err := add(path.Join(prefix, "metadata.json.sig"), skill.Signature, 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeReleaseArtifact(output, name string, value []byte, mode fs.FileMode) error {
	path := filepath.Join(output, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errors.New("release artifact directory could not be created")
	}
	if err := os.WriteFile(path, value, mode); err != nil {
		return errors.New("release artifact could not be written")
	}
	return nil
}

func buildTargetArtifacts(
	input buildInput,
	target buildTarget,
	assetBase string,
	license []byte,
	guide []byte,
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
	prebuiltPath := filepath.Join(input.kadoPrebuilt, versionedBinaryName)
	binary, err := os.ReadFile(prebuiltPath)
	if err != nil {
		return releaseclient.Target{}, fmt.Errorf(
			"Kado binary is unavailable for %s/%s",
			target.goos,
			target.goarch,
		)
	}
	if err := os.WriteFile(binaryPath, binary, 0o755); err != nil {
		return releaseclient.Target{}, errors.New("release binary could not be written")
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
	a2aArtifactName := executableArtifactName("kado-a2a", input.source.Version, target)
	a2aBinary, err := os.ReadFile(filepath.Join(input.a2aPrebuilt, a2aArtifactName))
	if err != nil || len(a2aBinary) == 0 {
		return releaseclient.Target{}, fmt.Errorf(
			"A2A binary is unavailable for %s/%s",
			target.goos,
			target.goarch,
		)
	}

	sbomName := base + ".spdx.json"
	sbom, err := makeSBOM(input, target, sbomName, binary, a2aBinary)
	if err != nil {
		return releaseclient.Target{}, err
	}
	sbomFile, err := add(sbomName, sbom, 0o644)
	if err != nil {
		return releaseclient.Target{}, err
	}
	archiveName := base + ".tar.gz"
	var archive []byte
	if archiveFormat == "zip" {
		archiveName = base + ".zip"
		archive, err = makeZip(input.builtAt, binaryName, binary, a2aBinary, license, input.a2aLicense, guide)
	} else {
		archive, err = makeTarGzip(input.builtAt, binaryName, binary, a2aBinary, license, input.a2aLicense, guide)
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
		OS:      target.goos,
		Arch:    target.goarch,
		Archive: archiveFile,
		Sidecar: releaseclient.EmbeddedArtifact{
			SHA256: releaseclient.Digest(a2aBinary),
			Size:   int64(len(a2aBinary)),
		},
		SBOM: sbomFile,
	}, nil
}

func makeTarGzip(
	builtAt time.Time,
	binaryName string,
	binary []byte,
	a2aBinary []byte,
	license []byte,
	a2aLicense []byte,
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
		{name: a2aExecutableForArchive(binaryName), mode: 0o755, data: a2aBinary},
		{name: "LICENSE", mode: 0o644, data: license},
		{name: "LICENSE-A2A-CLI", mode: 0o644, data: a2aLicense},
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
	a2aBinary []byte,
	license []byte,
	a2aLicense []byte,
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
		{name: a2aExecutableForArchive(binaryName), mode: 0o755, data: a2aBinary},
		{name: "LICENSE", mode: 0o644, data: license},
		{name: "LICENSE-A2A-CLI", mode: 0o644, data: a2aLicense},
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

func a2aExecutableForArchive(binaryName string) string {
	if binaryName == "kado.exe" {
		return "kado-a2a.exe"
	}
	return "kado-a2a"
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
