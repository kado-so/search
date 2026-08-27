package main

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/kado-so/search/internal/releaseclient"
)

const a2aDisplayPackage = a2aModule + "/internal/cli"

type executableBuild struct {
	KadoDirectory string
	A2ADirectory  string
	A2ALicense    []byte
}

func buildExecutables(
	root string,
	workspace string,
	goBinary string,
	source releaseIdentity,
	commit string,
	builtAt time.Time,
	publicKey ed25519.PublicKey,
	keyID string,
	metadataURL string,
	installChannel string,
	targets []buildTarget,
	a2a a2aPreparedSource,
) (executableBuild, error) {
	wantToolchain, err := pinnedGoVersionFromRoot(root)
	if err != nil || wantToolchain != a2a.Lock.GoToolchain {
		return executableBuild{}, errors.New("Kado and A2A must use the same pinned Go toolchain")
	}
	a2aLicense, err := os.ReadFile(filepath.Join(a2a.Root, filepath.FromSlash(a2a.Lock.License.Path)))
	if err != nil || len(a2aLicense) == 0 || releaseclient.Digest(a2aLicense) != a2a.Lock.License.SHA256 {
		return executableBuild{}, errors.New("A2A license does not match the source lock")
	}
	a2aOutput := filepath.Join(workspace, "a2a-binaries")
	kadoOutput := filepath.Join(workspace, "kado-binaries")
	if err := os.MkdirAll(a2aOutput, 0o755); err != nil {
		return executableBuild{}, errors.New("A2A binary staging could not be created")
	}
	if err := os.MkdirAll(kadoOutput, 0o755); err != nil {
		return executableBuild{}, errors.New("Kado binary staging could not be created")
	}
	baseEnvironment := withReleaseEnvironment(sanitizedEnvironment(), map[string]string{
		"CGO_ENABLED":       "0",
		"GOFLAGS":           "-mod=readonly",
		"GOTOOLCHAIN":       "local",
		"SOURCE_DATE_EPOCH": strconv.FormatInt(builtAt.Unix(), 10),
	})
	for _, target := range targets {
		if err := buildA2AExecutable(a2a, a2aOutput, goBinary, baseEnvironment, source.Version, builtAt, target); err != nil {
			return executableBuild{}, err
		}
	}
	for _, target := range targets {
		if err := buildKadoExecutable(
			root,
			kadoOutput,
			a2aOutput,
			goBinary,
			baseEnvironment,
			source,
			commit,
			builtAt,
			publicKey,
			keyID,
			metadataURL,
			installChannel,
			a2a,
			target,
		); err != nil {
			return executableBuild{}, err
		}
	}
	return executableBuild{
		KadoDirectory: kadoOutput,
		A2ADirectory:  a2aOutput,
		A2ALicense:    a2aLicense,
	}, nil
}

func buildA2AExecutable(
	a2a a2aPreparedSource,
	output string,
	goBinary string,
	baseEnvironment []string,
	bundleVersion string,
	builtAt time.Time,
	target buildTarget,
) error {
	name := executableArtifactName("kado-a2a", bundleVersion, target)
	environment := withReleaseEnvironment(baseEnvironment, map[string]string{
		"GOOS": target.goos, "GOARCH": target.goarch,
	})
	ldflags := "-s -w -buildid=" +
		" -X " + a2aDisplayPackage + ".version=" + a2a.Lock.Version +
		" -X " + a2aDisplayPackage + ".commit=" + a2a.Lock.Commit +
		" -X " + a2aDisplayPackage + ".date=" + builtAt.Format(time.RFC3339) +
		" -X '" + a2aDisplayPackage + ".displayName=" + a2a.Lock.DisplayName + "'"
	if _, err := commandOutput(
		a2a.Root,
		environment,
		goBinary,
		"build",
		"-mod=readonly",
		"-trimpath",
		"-buildvcs=false",
		"-ldflags",
		ldflags,
		"-o",
		filepath.Join(output, name),
		".",
	); err != nil {
		return fmt.Errorf("A2A build failed for %s/%s", target.goos, target.goarch)
	}
	return nil
}

func buildKadoExecutable(
	root string,
	output string,
	a2aOutput string,
	goBinary string,
	baseEnvironment []string,
	source releaseIdentity,
	commit string,
	builtAt time.Time,
	publicKey ed25519.PublicKey,
	keyID string,
	metadataURL string,
	installChannel string,
	a2a a2aPreparedSource,
	target buildTarget,
) error {
	a2aName := executableArtifactName("kado-a2a", source.Version, target)
	a2aBinary, err := os.ReadFile(filepath.Join(a2aOutput, a2aName))
	if err != nil || len(a2aBinary) == 0 {
		return fmt.Errorf("A2A executable is unavailable for %s/%s", target.goos, target.goarch)
	}
	environment := withReleaseEnvironment(baseEnvironment, map[string]string{
		"GOOS": target.goos, "GOARCH": target.goarch,
	})
	buildinfoPackage := "github.com/kado-so/search/internal/buildinfo."
	ldflags := "-s -w -buildid=" +
		" -X " + buildinfoPackage + "Version=" + source.Version +
		" -X " + buildinfoPackage + "Commit=" + commit +
		" -X " + buildinfoPackage + "Date=" + builtAt.Format(time.RFC3339) +
		" -X " + buildinfoPackage + "Target=" + target.goos + "/" + target.goarch +
		" -X " + buildinfoPackage + "ReleasePublicKey=" + releaseclient.PublicKeyText(publicKey) +
		" -X " + buildinfoPackage + "ReleaseKeyID=" + keyID +
		" -X " + buildinfoPackage + "ReleaseMetadataURL=" + metadataURL +
		" -X " + buildinfoPackage + "InstallChannel=" + installChannel +
		" -X " + buildinfoPackage + "A2AVersion=" + a2a.Lock.Version +
		" -X " + buildinfoPackage + "A2ATag=" + a2aTagValue(a2a.Lock.Tag) +
		" -X " + buildinfoPackage + "A2AUpstreamCommit=" + a2a.Lock.Commit +
		" -X " + buildinfoPackage + "A2ADate=" + builtAt.Format(time.RFC3339) +
		" -X " + buildinfoPackage + "A2ATarget=" + target.goos + "/" + target.goarch +
		" -X " + buildinfoPackage + "A2APatchSet=sha256:" + a2a.PatchSetSHA256 +
		" -X " + buildinfoPackage + "A2AArtifactSHA256=" + releaseclient.Digest(a2aBinary) +
		" -X " + buildinfoPackage + "A2AArtifactSize=" + strconv.Itoa(len(a2aBinary))
	if _, err := commandOutput(
		root,
		environment,
		goBinary,
		"build",
		"-mod=readonly",
		"-trimpath",
		"-buildvcs=false",
		"-ldflags",
		ldflags,
		"-o",
		filepath.Join(output, executableArtifactName("kado", source.Version, target)),
		"./cmd/kado",
	); err != nil {
		return fmt.Errorf("Kado build failed for %s/%s", target.goos, target.goarch)
	}
	return nil
}

func executableArtifactName(component, version string, target buildTarget) string {
	suffix := ""
	if target.goos == "windows" {
		suffix = ".exe"
	}
	return fmt.Sprintf("%s_%s_%s_%s%s", component, version, target.goos, target.goarch, suffix)
}

func a2aTagValue(tag string) string {
	if tag == "" {
		return "none"
	}
	return tag
}

func pinnedGoVersionFromRoot(root string) (string, error) {
	configured, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return "", err
	}
	return pinnedGoVersion(configured)
}
