// Command release builds deterministic, signed Kado CLI release bundles.
package main

import (
	"crypto/ed25519"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kado-so/search/internal/releaseclient"
)

const signingKeyEnvironment = "KADO_RELEASE_SIGNING_KEY"

var commitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

type options struct {
	root     string
	output   string
	commit   string
	epoch    int64
	goBinary string
}

type distributionSource struct {
	Plugin struct {
		Version    string `json:"version"`
		Repository string `json:"repository"`
		Homepage   string `json:"homepage"`
	} `json:"plugin"`
	Installation struct {
		CLIExecutable string `json:"cli_executable"`
		CLIInstallURL string `json:"cli_install_url"`
		Source        struct {
			Kind       string `json:"kind"`
			Repository string `json:"repository"`
		} `json:"source"`
	} `json:"installation"`
}

func main() {
	var configured options
	flag.StringVar(&configured.root, "root", ".", "repository root")
	flag.StringVar(&configured.output, "out", "dist/release", "output directory")
	flag.StringVar(&configured.commit, "commit", "", "exact 40-character source commit")
	flag.Int64Var(&configured.epoch, "source-date-epoch", 0, "reproducible UTC build timestamp")
	flag.StringVar(&configured.goBinary, "go", "go", "Go executable")
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
	source, err := loadDistribution(root)
	if err != nil {
		return err
	}
	if err := verifyGoVersion(root, configured.goBinary); err != nil {
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
		goBinary:   configured.goBinary,
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
		"release dry-run complete version=%s commit=%s targets=6 output=%s\n",
		source.Plugin.Version,
		configured.commit,
		output,
	)
	return nil
}

func loadDistribution(root string) (distributionSource, error) {
	path := filepath.Join(root, "distribution", "kado-search.manifest.json")
	encoded, err := os.ReadFile(path)
	if err != nil {
		return distributionSource{}, errors.New("canonical distribution metadata is unavailable")
	}
	var source distributionSource
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	if err := decoder.Decode(&source); err != nil {
		return distributionSource{}, errors.New("canonical distribution metadata is invalid")
	}
	if source.Plugin.Version == "" ||
		source.Plugin.Repository != "https://github.com/kado-so/search" ||
		source.Plugin.Homepage != source.Installation.CLIInstallURL ||
		source.Installation.CLIExecutable != "kado" ||
		source.Installation.CLIInstallURL != "https://kado.so/install" ||
		source.Installation.Source.Kind != "github" ||
		source.Installation.Source.Repository != "kado-so/search" {
		return distributionSource{}, errors.New("canonical distribution identity is invalid")
	}
	return source, nil
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
	configured, err := os.ReadFile(filepath.Join(root, ".prototools"))
	if err != nil {
		return errors.New("pinned Go toolchain configuration is unavailable")
	}
	match := regexp.MustCompile(`(?m)^go = "([^"]+)"$`).FindSubmatch(configured)
	if len(match) != 2 {
		return errors.New("pinned Go toolchain version is invalid")
	}
	output, err := commandOutput(root, nil, goBinary, "env", "GOVERSION")
	if err != nil {
		return errors.New("Go toolchain version could not be read")
	}
	actual := strings.TrimSpace(string(output))
	want := "go" + string(match[1])
	if actual != want {
		return fmt.Errorf("release requires pinned Go toolchain %s", want)
	}
	return nil
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

func epochText(value time.Time) string {
	return strconv.FormatInt(value.Unix(), 10)
}
