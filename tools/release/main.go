// Command release finalizes and signs prebuilt Kado CLI release bundles.
package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/kado-so/search/internal/releaseclient"
)

const signingKeyEnvironment = "KADO_RELEASE_SIGNING_KEY"

const (
	releaseRepository = "https://github.com/kado-so/search"
	releaseInstallURL = "https://kado.so/install"
	releaseExecutable = "kado"
)

var (
	commitPattern         = regexp.MustCompile(`^[0-9a-f]{40}$`)
	releaseVersionPattern = regexp.MustCompile(
		`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`,
	)
)

type options struct {
	root          string
	output        string
	prebuilt      string
	commit        string
	epoch         int64
	version       string
	goreleaserEnv string
}

type releaseIdentity struct {
	Version    string
	Repository string
	InstallURL string
	Executable string
}

func main() {
	var configured options
	flag.StringVar(&configured.root, "root", ".", "repository root")
	flag.StringVar(&configured.output, "out", "dist/release", "output directory")
	flag.StringVar(
		&configured.prebuilt,
		"prebuilt",
		"",
		"directory containing GoReleaser-built binaries",
	)
	flag.StringVar(&configured.commit, "commit", "", "exact 40-character source commit")
	flag.Int64Var(&configured.epoch, "source-date-epoch", 0, "release UTC build timestamp")
	flag.StringVar(&configured.version, "version", "", "semantic release version")
	flag.StringVar(
		&configured.goreleaserEnv,
		"write-goreleaser-env",
		"",
		"append public build metadata to a GoReleaser environment file",
	)
	flag.Parse()
	if flag.NArg() != 0 {
		fatal(errors.New("release does not accept positional arguments"))
	}
	if err := run(configured); err != nil {
		fatal(err)
	}
}

func run(configured options) error {
	root, err := filepath.Abs(configured.root)
	if err != nil {
		return errors.New("release root is invalid")
	}
	output := configured.output
	if !filepath.IsAbs(output) {
		output = filepath.Join(root, output)
	}
	if configured.epoch <= 0 {
		return errors.New("--source-date-epoch is required")
	}
	if !commitPattern.MatchString(configured.commit) {
		return errors.New("--commit must be an exact lowercase 40-character Git commit")
	}
	source, err := newReleaseIdentity(configured.version)
	if err != nil {
		return err
	}
	if err := verifyGoVersion(root, "go"); err != nil {
		return err
	}
	private, err := signingKeyFromEnvironment()
	if err != nil {
		return err
	}
	public := private.Public().(ed25519.PublicKey)
	keyID, err := releaseclient.KeyID(public)
	if err != nil {
		return errors.New("release signing key is invalid")
	}
	publicDER, err := x509.MarshalPKIXPublicKey(public)
	if err != nil {
		return errors.New("release signing key is invalid")
	}
	publicPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "PUBLIC KEY",
		Bytes: publicDER,
	})
	if len(publicPEM) == 0 {
		return errors.New("release signing key is invalid")
	}
	builtAt := time.Unix(configured.epoch, 0).UTC()
	if builtAt.Year() < 1980 {
		return errors.New("--source-date-epoch must be in 1980 or later")
	}
	metadataURL := strings.TrimSuffix(source.InstallURL, "/") +
		"/releases/stable/release-metadata.json"
	if configured.goreleaserEnv != "" {
		return writeGoReleaserEnvironment(
			configured.goreleaserEnv,
			source,
			configured.commit,
			builtAt,
			releaseclient.PublicKeyText(public),
			keyID,
			metadataURL,
		)
	}
	if configured.prebuilt == "" {
		return errors.New("--prebuilt is required")
	}
	prebuilt := configured.prebuilt
	if !filepath.IsAbs(prebuilt) {
		prebuilt = filepath.Join(root, prebuilt)
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return errors.New("release output parent could not be created")
	}
	staging, err := os.MkdirTemp(filepath.Dir(output), ".kado-release-*")
	if err != nil {
		return errors.New("release staging directory could not be created")
	}
	defer os.RemoveAll(staging)
	if err := buildRelease(buildInput{
		root:       root,
		output:     staging,
		prebuilt:   prebuilt,
		goBinary:   "go",
		source:     source,
		commit:     configured.commit,
		builtAt:    builtAt,
		privateKey: private,
		publicKey:  public,
		publicPEM:  publicPEM,
		keyID:      keyID,
	}); err != nil {
		return err
	}
	if err := replaceOutput(staging, output); err != nil {
		return err
	}
	fmt.Printf(
		"release finalized version=%s commit=%s targets=6 output=%s\n",
		source.Version,
		configured.commit,
		output,
	)
	return nil
}

func writeGoReleaserEnvironment(
	path string,
	source releaseIdentity,
	commit string,
	builtAt time.Time,
	publicKey string,
	keyID string,
	metadataURL string,
) error {
	values := []string{
		"KADO_RELEASE_VERSION=" + source.Version,
		"KADO_RELEASE_COMMIT=" + commit,
		"KADO_RELEASE_DATE=" + builtAt.Format(time.RFC3339),
		"KADO_RELEASE_PUBLIC_KEY=" + publicKey,
		"KADO_RELEASE_KEY_ID=" + keyID,
		"KADO_RELEASE_METADATA_URL=" + metadataURL,
	}
	for _, value := range values {
		if strings.ContainsAny(value, "\r\n") {
			return errors.New("GoReleaser environment value is invalid")
		}
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return errors.New("GoReleaser environment file could not be opened")
	}
	defer file.Close()
	if _, err := file.WriteString(strings.Join(values, "\n") + "\n"); err != nil {
		return errors.New("GoReleaser environment file could not be written")
	}
	return file.Sync()
}

func newReleaseIdentity(version string) (releaseIdentity, error) {
	if len(version) > 48 || !releaseVersionPattern.MatchString(version) {
		return releaseIdentity{}, errors.New("--version must be a semantic version")
	}
	return releaseIdentity{
		Version:    version,
		Repository: releaseRepository,
		InstallURL: releaseInstallURL,
		Executable: releaseExecutable,
	}, nil
}

func signingKeyFromEnvironment() (ed25519.PrivateKey, error) {
	text := strings.TrimSpace(os.Getenv(signingKeyEnvironment))
	if text == "" {
		return nil, fmt.Errorf(
			"%s must contain a base64-encoded 32-byte Ed25519 seed",
			signingKeyEnvironment,
		)
	}
	seed, err := base64.StdEncoding.DecodeString(text)
	if err != nil {
		seed, err = base64.RawStdEncoding.DecodeString(text)
	}
	if err != nil || len(seed) != ed25519.SeedSize {
		return nil, fmt.Errorf("%s is invalid", signingKeyEnvironment)
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

func verifyGoVersion(root, goBinary string) error {
	configured, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		return errors.New("pinned Go toolchain configuration is unavailable")
	}
	want, err := pinnedGoVersion(configured)
	if err != nil {
		return errors.New("pinned Go toolchain version is invalid")
	}
	output, err := commandOutput(root, nil, goBinary, "env", "GOVERSION")
	if err != nil {
		return errors.New("Go toolchain version could not be read")
	}
	actual := strings.TrimSpace(string(output))
	if actual != want {
		return fmt.Errorf("release requires pinned Go toolchain %s", want)
	}
	return nil
}

func pinnedGoVersion(goMod []byte) (string, error) {
	match := regexp.MustCompile(
		`(?m)^toolchain[ \t]+(go[0-9]+\.[0-9]+\.[0-9]+)[ \t]*$`,
	).FindSubmatch(goMod)
	if len(match) != 2 {
		return "", errors.New("go.mod must contain an exact toolchain directive")
	}
	return string(match[1]), nil
}

func replaceOutput(staging, output string) error {
	if info, err := os.Lstat(output); err == nil {
		if !info.IsDir() {
			return errors.New("release output path is not a directory")
		}
		entries, readErr := os.ReadDir(output)
		if readErr != nil || len(entries) != 0 {
			return errors.New("release output directory must be absent or empty")
		}
		if err := os.Remove(output); err != nil {
			return errors.New("empty release output directory could not be replaced")
		}
	} else if !os.IsNotExist(err) {
		return errors.New("release output path could not be inspected")
	}
	if err := os.MkdirAll(filepath.Dir(output), 0o755); err != nil {
		return errors.New("release output parent could not be created")
	}
	if err := os.Rename(staging, output); err != nil {
		return errors.New("release output could not be installed atomically")
	}
	return nil
}

func fatal(err error) {
	fmt.Fprintf(os.Stderr, "kado-release: %s\n", err)
	os.Exit(1)
}
