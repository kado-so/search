package launcher

import (
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"

	"github.com/kado-so/search/internal/payload"
)

const completeActivations = "activations-v3"

var ErrBusy = errors.New("MCP installation is busy; retained payloads and credentials")

type completeActivation struct {
	Schema         int    `json:"schema"`
	Generation     uint64 `json:"generation"`
	Version        string `json:"version"`
	ManifestSHA256 string `json:"manifest_sha256"`
}

// CompletePaths always refers to one authenticated version directory.
type CompletePaths struct {
	Root     string
	Manifest payload.Manifest
	Digest   string
}

func (p CompletePaths) Entry(role string) string {
	return filepath.Join(p.Root, filepath.FromSlash(p.Manifest.Entries[role]))
}

func HasCompleteInstallation(launcher string) bool {
	_, err := os.Lstat(filepath.Join(managedRoot(launcher), completeActivations))
	return !errors.Is(err, fs.ErrNotExist)
}

func readCompleteActivation(root, name string) (completeActivation, error) {
	var a completeActivation
	if !activationPattern.MatchString(name) {
		return a, payload.ErrInvalid
	}
	b, err := payload.ReadFile(root, completeActivations+"/"+name, maxActivationSize)
	if err != nil {
		return a, err
	}
	if json.Unmarshal(b, &a) != nil {
		return a, payload.ErrInvalid
	}
	canonical, _ := json.Marshal(a)
	if string(b) != string(canonical)+"\n" || a.Schema != 3 || !payload.ValidVersion(a.Version) || !payload.ValidDigest(a.ManifestSHA256) {
		return a, payload.ErrInvalid
	}
	generation, err := strconv.ParseUint(strings.TrimSuffix(name, ".json"), 10, 64)
	if err != nil || generation != a.Generation {
		return a, payload.ErrInvalid
	}
	return a, nil
}

func completePaths(root string, a completeActivation, key ed25519.PublicKey) (CompletePaths, error) {
	dir := filepath.Join(root, "versions", a.Version)
	m, err := payload.VerifyTree(dir, a.ManifestSHA256, key)
	if err != nil || m.Version != a.Version || m.Target != runtime.GOOS+"/"+runtime.GOARCH {
		return CompletePaths{}, payload.ErrInvalid
	}
	return CompletePaths{Root: dir, Manifest: m, Digest: a.ManifestSHA256}, nil
}

// ActiveComplete selects the newest fully valid signed unit, never components
// independently. No legacy activation is considered when this format is present.
func ActiveComplete(launcher string, key ed25519.PublicKey) (CompletePaths, error) {
	root := managedRoot(launcher)
	if err := payload.PlainPath(root); err != nil {
		return CompletePaths{}, err
	}
	directory := filepath.Join(root, completeActivations)
	if err := payload.PlainPath(directory); err != nil {
		return CompletePaths{}, err
	}
	entries, err := os.ReadDir(directory)
	if err != nil {
		return CompletePaths{}, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	for _, e := range entries {
		a, err := readCompleteActivation(root, e.Name())
		if err != nil {
			continue
		}
		p, err := completePaths(root, a, key)
		if err == nil {
			return p, nil
		}
	}
	return CompletePaths{}, payload.ErrInvalid
}

// InstallCompleteLocked publishes a staged complete unit with a single atomic
// activation rename. Caller holds WithUpdateLock. Credentials are never copied.
func InstallCompleteLocked(launcher string, b payload.Bundle, key ed25519.PublicKey) error {
	return installCompleteWithRecords(launcher, b, key, atomicCompleteFile)
}

// Record writes are the durable transaction boundary. Tests inject storage
// failures here without changing archive verification or activation selection.
func installCompleteWithRecords(launcher string, b payload.Bundle, key ed25519.PublicKey, writeRecord func(string, []byte) error) error {
	m, err := payload.Authenticate(b.Encoded, b.Signature, key)
	if err != nil || m.Target != runtime.GOOS+"/"+runtime.GOARCH {
		return payload.ErrInvalid
	}
	if !directInstallation(launcher) {
		return errors.New("installation is package managed")
	}
	root := managedRoot(launcher)
	if err := ensureLayout(root); err != nil {
		return err
	}
	if err := payload.PlainPath(root); err != nil {
		return err
	}
	for _, name := range []string{completeActivations, "identities-v3"} {
		p := filepath.Join(root, name)
		if err := os.Mkdir(p, 0700); err != nil && !errors.Is(err, fs.ErrExist) {
			return err
		}
		if err := payload.PlainPath(p); err != nil {
			return err
		}
	}
	// Inventory is initialized only for a previously unregistered installation.
	// Never replace an existing inventory, including corrupt or inaccessible ones.
	if err := initializeHomes(root); err != nil {
		return err
	}
	if _, err := os.Lstat(filepath.Join(filepath.Dir(launcher), receiptName)); os.IsNotExist(err) {
		if err := writeDirectReceipt(launcher); err != nil {
			return err
		}
	}
	digest := payload.Digest(b.Encoded)
	priorRoot := filepath.Join(root, "versions", m.Version)
	if encoded, err := payload.ReadFile(priorRoot, payload.ManifestName, payload.MaxManifest); err == nil {
		signature, err := payload.ReadFile(priorRoot, payload.SignatureName, ed25519.SignatureSize)
		if err == nil {
			if old, err := payload.Authenticate(encoded, signature, key); err == nil && old.Version == m.Version && payload.Digest(encoded) != digest {
				return errors.New("version already identifies different payload bytes")
			}
		}
	}
	prior, err := os.ReadDir(filepath.Join(root, completeActivations))
	if err != nil {
		return err
	}
	for _, entry := range prior {
		a, err := readCompleteActivation(root, entry.Name())
		if err == nil && a.Version == m.Version && a.ManifestSHA256 != digest {
			return errors.New("version already identifies different payload bytes")
		}
	}
	identity := filepath.Join(root, "identities-v3", m.Version+".json")
	if old, err := payload.ReadFile(root, "identities-v3/"+m.Version+".json", 128); err == nil {
		if string(old) != digest+"\n" {
			return errors.New("version already identifies different payload bytes")
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	versions := filepath.Join(root, "versions")
	stage, err := os.MkdirTemp(versions, ".kado-version-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	if err := payload.WriteTree(stage, b, key); err != nil {
		return err
	}
	if err := syncPayloadDirectories(stage); err != nil {
		return err
	}
	// Windows does not permit replacing an executable that the maintenance
	// process is using. Run it from a separate verified temporary copy, then
	// remove that copy only after its leases and process have ended.
	maintenance, err := os.MkdirTemp(versions, ".kado-maintenance-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(maintenance)
	if err := payload.WriteTree(maintenance, b, key); err != nil {
		return err
	}
	staged := CompletePaths{Root: maintenance, Manifest: m, Digest: digest}
	return withPayloadMaintenance(root, staged, key, false, func(refs map[string]bool, alive func() error) error {
		destination := filepath.Join(versions, m.Version)
		_, valid := payload.VerifyTree(destination, digest, key)
		if valid != nil {
			if err := alive(); err != nil {
				return err
			}
			if refs[destination] {
				return ErrBusy
			}
			backup := ""
			if _, err := os.Lstat(destination); err == nil {
				if err := payload.PlainPath(destination); err != nil {
					return err
				}
				backup, err = os.MkdirTemp(versions, ".kado-repair-")
				if err != nil {
					return err
				}
				if err := os.Remove(backup); err != nil {
					return err
				}
				if err := commitRename(destination, backup); err != nil {
					return err
				}
			} else if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			if err := commitRename(stage, destination); err != nil {
				if backup != "" {
					_ = commitRename(backup, destination)
				}
				return err
			}
			// Keep the backup until publication succeeds. Failure leaves the former
			// activations usable, or the same signed repaired version in place.
			if backup != "" {
				defer os.RemoveAll(backup)
			}
			if err := syncDirectory(versions); err != nil {
				return err
			}
		}
		if _, err := payload.VerifyTree(destination, digest, key); err != nil {
			return err
		}
		if err := alive(); err != nil {
			return err
		}
		if _, err := os.Lstat(identity); errors.Is(err, fs.ErrNotExist) {
			if err := writeRecord(identity, []byte(digest+"\n")); err != nil {
				return err
			}
		}
		active, activeErr := ActiveComplete(launcher, key)
		if activeErr != nil || active.Digest != digest || active.Manifest.Version != m.Version {
			generation, err := nextActivationGeneration(filepath.Join(root, completeActivations))
			if err != nil {
				return err
			}
			a := completeActivation{Schema: 3, Generation: generation, Version: m.Version, ManifestSHA256: digest}
			encoded, _ := json.Marshal(a)
			if err := writeRecord(filepath.Join(root, completeActivations, fmt.Sprintf("%020d.json", generation)), append(encoded, '\n')); err != nil {
				return err
			}
		}
		// Retention errors are reported rather than claiming cleanup succeeded.
		refs[maintenance] = true
		return pruneComplete(root, key, refs, alive)
	})
}

func atomicCompleteFile(destination string, value []byte) error {
	dir := filepath.Dir(destination)
	f, err := os.CreateTemp(dir, ".kado-transaction-")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if err = f.Chmod(0600); err == nil {
		_, err = f.Write(value)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := commitRename(name, destination); err != nil {
		return err
	}
	return syncDirectory(dir)
}

func initializeHomes(root string) error {
	p := filepath.Join(root, "registered-homes-v1.json")
	if _, err := os.Lstat(p); err == nil {
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	// Existing complete activations without their inventory are not a fresh
	// installation. Reconstructing from the default home could lose live users.
	entries, err := os.ReadDir(filepath.Join(root, completeActivations))
	if err != nil {
		return err
	}
	for _, e := range entries {
		if activationPattern.MatchString(e.Name()) {
			return ErrBusy
		}
	}
	return atomicCompleteFile(p, []byte("{\"schema\":1,\"homes\":[]}\n"))
}

func syncPayloadDirectories(root string) error {
	var dirs []string
	if err := filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if e.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	}); err != nil {
		return err
	}
	for i := len(dirs) - 1; i >= 0; i-- {
		if err := syncDirectory(dirs[i]); err != nil {
			return err
		}
	}
	return nil
}

func pruneComplete(root string, key ed25519.PublicKey, refs map[string]bool, alive func() error) error {
	entries, err := os.ReadDir(filepath.Join(root, completeActivations))
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() > entries[j].Name() })
	keep := map[string]bool{}
	for p := range refs {
		keep[p] = true
	}
	validCount := 0
	for _, e := range entries {
		a, err := readCompleteActivation(root, e.Name())
		if err != nil {
			continue
		}
		p, err := completePaths(root, a, key)
		if err != nil {
			continue
		}
		if validCount < retainedVersions {
			keep[p.Root] = true
			validCount++
		}
	}
	versions := filepath.Join(root, "versions")
	installed, err := os.ReadDir(versions)
	if err != nil {
		return err
	}
	for _, e := range installed {
		temporary := strings.HasPrefix(e.Name(), ".kado-version-") || strings.HasPrefix(e.Name(), ".kado-maintenance-") || strings.HasPrefix(e.Name(), ".kado-repair-")
		if !payload.ValidVersion(e.Name()) && !temporary {
			continue
		}
		p := filepath.Join(versions, e.Name())
		if keep[p] {
			continue
		}
		if err := payload.PlainPath(p); err != nil {
			return err
		}
		if err := removePayloadTree(p, alive); err != nil {
			return err
		}
	}
	return syncDirectory(versions)
}
