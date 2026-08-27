package launcher

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestBundleActivationAuthenticatesCompletePair(t *testing.T) {
	t.Parallel()
	launcherPath := testLauncher(t, []byte("stable"))
	bundle := bundleFixture("one")
	installBundleForTest(t, launcherPath, "1.0.0", bundle)

	paths, version, err := ActiveBundle(launcherPath)
	if err != nil || version != "1.0.0" {
		t.Fatalf("ActiveBundle() paths=%#v version=%q error=%v", paths, version, err)
	}
	assertContent(t, paths.Kado, bundle.Kado)
	assertContent(t, paths.A2A, bundle.A2A)
	assertContent(t, launcherPath, []byte("stable"))

	record := newestActivation(t, launcherPath)
	if record.SchemaVersion != activationVersionV2 || record.Files == nil ||
		record.Files.Kado.Path != bundleKadoRelativePath(version) ||
		record.Files.A2A.Path != bundleA2ARelativePath(version) ||
		record.Files.Kado.Size != int64(len(bundle.Kado)) ||
		record.Files.A2A.Size != int64(len(bundle.A2A)) ||
		record.Files.Kado.SHA256 != digest(bundle.Kado) ||
		record.Files.A2A.SHA256 != digest(bundle.A2A) {
		t.Fatalf("activation = %#v", record)
	}
}

func TestBundleReaderFallsBackWhenEitherNewestExecutableIsInvalid(t *testing.T) {
	for _, role := range []string{"kado", "a2a"} {
		role := role
		t.Run(role, func(t *testing.T) {
			t.Parallel()
			launcherPath := testLauncher(t, []byte("stable"))
			older := bundleFixture("older")
			newer := bundleFixture("newer")
			installBundleForTest(t, launcherPath, "1.0.0", older)
			installBundleForTest(t, launcherPath, "1.1.0", newer)
			paths, _, err := ActiveBundle(launcherPath)
			if err != nil {
				t.Fatal(err)
			}
			target := paths.Kado
			if role == "a2a" {
				target = paths.A2A
			}
			if err := os.WriteFile(target, []byte("tampered"), 0o755); err != nil {
				t.Fatal(err)
			}
			fallback, version, err := ActiveBundle(launcherPath)
			if err != nil || version != "1.0.0" {
				t.Fatalf("fallback=%#v version=%q error=%v", fallback, version, err)
			}
			assertContent(t, fallback.Kado, older.Kado)
			assertContent(t, fallback.A2A, older.A2A)
		})
	}
}

func TestBundleReaderIgnoresPartialDirectoryAndMalformedActivation(t *testing.T) {
	t.Parallel()
	launcherPath := testLauncher(t, []byte("stable"))
	bundle := bundleFixture("older")
	installBundleForTest(t, launcherPath, "1.0.0", bundle)
	root := managedRoot(launcherPath)
	interruptedTemporary := filepath.Join(root, "versions", ".kado-version-interrupted")
	if err := os.Mkdir(interruptedTemporary, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(interruptedTemporary, executableName()), []byte("temporary-kado"), 0o755); err != nil {
		t.Fatal(err)
	}
	partial := filepath.Join(root, "versions", "1.1.0")
	if err := os.Mkdir(partial, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(partial, executableName()), []byte("only-kado"), 0o755); err != nil {
		t.Fatal(err)
	}
	activationDirectory := filepath.Join(root, "activations")
	if err := os.WriteFile(filepath.Join(activationDirectory, ".kado-activation-interrupted"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activationDirectory, fmt.Sprintf("%020d.json", 98)), []byte("{broken\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	partialRecord := activation{
		SchemaVersion: activationVersionV2,
		Generation:    99,
		Version:       "1.1.0",
		Files: &activationFiles{
			Kado: activationFile{Path: bundleKadoRelativePath("1.1.0"), Size: 9, SHA256: digest([]byte("only-kado"))},
			A2A:  activationFile{Path: bundleA2ARelativePath("1.1.0"), Size: 7, SHA256: digest([]byte("missing"))},
		},
	}
	encoded, err := json.Marshal(partialRecord)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(activationDirectory, fmt.Sprintf("%020d.json", 99)), append(encoded, '\n'), 0o600); err != nil {
		t.Fatal(err)
	}

	_, version, err := ActiveBundle(launcherPath)
	if err != nil || version != "1.0.0" {
		t.Fatalf("ActiveBundle() version=%q error=%v", version, err)
	}
}

func TestBundleReaderRejectsMalformedClosedRecords(t *testing.T) {
	validFiles := func(version string) *activationFiles {
		return &activationFiles{
			Kado: activationFile{Path: bundleKadoRelativePath(version), Size: 4, SHA256: digest([]byte("kado"))},
			A2A:  activationFile{Path: bundleA2ARelativePath(version), Size: 3, SHA256: digest([]byte("a2a"))},
		}
	}
	for _, test := range []struct {
		name   string
		mutate func(*activation)
	}{
		{name: "wrong path", mutate: func(value *activation) { value.Files.A2A.Path = "versions/1.1.0/other" }},
		{name: "zero size", mutate: func(value *activation) { value.Files.Kado.Size = 0 }},
		{name: "oversized", mutate: func(value *activation) { value.Files.A2A.Size = maxPayloadSize + 1 }},
		{name: "uppercase digest", mutate: func(value *activation) { value.Files.Kado.SHA256 = strings.ToUpper(value.Files.Kado.SHA256) }},
		{name: "legacy fields", mutate: func(value *activation) { value.Executable = payloadRelativePath(value.Version) }},
	} {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			launcherPath := testLauncher(t, []byte("stable"))
			installBundleForTest(t, launcherPath, "1.0.0", bundleFixture("older"))
			record := activation{SchemaVersion: activationVersionV2, Generation: 99, Version: "1.1.0", Files: validFiles("1.1.0")}
			test.mutate(&record)
			encoded, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(managedRoot(launcherPath), "activations", fmt.Sprintf("%020d.json", record.Generation))
			if err := os.WriteFile(path, append(encoded, '\n'), 0o600); err != nil {
				t.Fatal(err)
			}
			_, version, err := ActiveBundle(launcherPath)
			if err != nil || version != "1.0.0" {
				t.Fatalf("malformed newest record selected: version=%q error=%v", version, err)
			}
		})
	}
}

func TestBundleInstallIsIdempotentRejectsCollisionAndRepairsAuthenticatedPair(t *testing.T) {
	t.Parallel()
	launcherPath := testLauncher(t, []byte("stable"))
	bundle := bundleFixture("one")
	installBundleForTest(t, launcherPath, "1.0.0", bundle)
	before := activationCount(t, launcherPath)
	installBundleForTest(t, launcherPath, "1.0.0", bundle)
	if after := activationCount(t, launcherPath); after != before {
		t.Fatalf("idempotent install created %d activations, want %d", after, before)
	}
	if err := WithUpdateLock(launcherPath, func() error {
		return InstallBundleVersionLocked(launcherPath, "1.0.0", bundleFixture("different"))
	}); err == nil {
		t.Fatal("same-version conflicting pair was accepted")
	}
	paths, _, err := ActiveBundle(launcherPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.A2A, []byte("tampered"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ActiveBundle(launcherPath); err == nil {
		t.Fatal("tampered pair remained selectable")
	}
	installBundleForTest(t, launcherPath, "1.0.0", bundle)
	repaired, version, err := ActiveBundle(launcherPath)
	if err != nil || version != "1.0.0" {
		t.Fatalf("repair=%#v version=%q error=%v", repaired, version, err)
	}
	assertContent(t, repaired.A2A, bundle.A2A)
}

func TestConcurrentBundleReadersNeverObserveMixedVersions(t *testing.T) {
	launcherPath := testLauncher(t, []byte("stable"))
	older := bundleFixture("older")
	newer := bundleFixture("newer")
	installBundleForTest(t, launcherPath, "1.0.0", older)

	const readers = 16
	start := make(chan struct{})
	results := make(chan error, readers)
	var wait sync.WaitGroup
	for index := 0; index < readers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for attempt := 0; attempt < 30; attempt++ {
				paths, version, err := ActiveBundle(launcherPath)
				if err != nil {
					results <- err
					return
				}
				kado, kadoErr := os.ReadFile(paths.Kado)
				a2a, a2aErr := os.ReadFile(paths.A2A)
				if kadoErr != nil || a2aErr != nil {
					results <- fmt.Errorf("read pair: %v, %v", kadoErr, a2aErr)
					return
				}
				if version == "1.0.0" && string(kado) == string(older.Kado) && string(a2a) == string(older.A2A) ||
					version == "1.1.0" && string(kado) == string(newer.Kado) && string(a2a) == string(newer.A2A) {
					continue
				}
				results <- fmt.Errorf("mixed pair: version=%s kado=%q a2a=%q", version, kado, a2a)
				return
			}
			results <- nil
		}()
	}
	close(start)
	installBundleForTest(t, launcherPath, "1.1.0", newer)
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestConcurrentBundleInstallersPublishOneEquivalentActivation(t *testing.T) {
	launcherPath := testLauncher(t, []byte("stable"))
	installBundleForTest(t, launcherPath, "1.0.0", bundleFixture("older"))
	newer := bundleFixture("newer")

	const installers = 20
	results := make(chan error, installers)
	var wait sync.WaitGroup
	for index := 0; index < installers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			results <- WithUpdateLock(launcherPath, func() error {
				return InstallBundleVersionLocked(launcherPath, "1.1.0", newer)
			})
		}()
	}
	wait.Wait()
	close(results)
	for err := range results {
		if err != nil {
			t.Fatal(err)
		}
	}
	if count := activationCount(t, launcherPath); count != 2 {
		t.Fatalf("activation count = %d, want initial plus one update", count)
	}
	paths, version, err := ActiveBundle(launcherPath)
	if err != nil || version != "1.1.0" {
		t.Fatalf("active paths=%#v version=%q error=%v", paths, version, err)
	}
}

func TestBundleCleanupRetainsNewestTwoCompleteUnits(t *testing.T) {
	t.Parallel()
	launcherPath := testLauncher(t, []byte("stable"))
	for index, version := range []string{"1.0.0", "1.1.0", "1.2.0"} {
		installBundleForTest(t, launcherPath, version, bundleFixture(fmt.Sprint(index)))
	}
	entries, err := os.ReadDir(filepath.Join(managedRoot(launcherPath), "versions"))
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, entry := range entries {
		if entry.IsDir() {
			found[entry.Name()] = true
		}
	}
	if found["1.0.0"] || !found["1.1.0"] || !found["1.2.0"] || len(found) != 2 {
		t.Fatalf("retained versions = %#v", found)
	}
	for version := range found {
		for _, name := range []string{executableName(), a2aExecutableName()} {
			if _, err := os.Stat(filepath.Join(managedRoot(launcherPath), "versions", version, name)); err != nil {
				t.Fatalf("%s/%s missing: %v", version, name, err)
			}
		}
	}
}

func TestBundleBootstrapRequiresAndCopiesSiblingPair(t *testing.T) {
	t.Parallel()
	launcherPath := testLauncher(t, []byte("stable-kado"))
	sidecarPath := filepath.Join(filepath.Dir(launcherPath), a2aExecutableName())
	if err := os.WriteFile(sidecarPath, []byte("stable-a2a"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload, version, err := ensureBootstrap(launcherPath, "1.0.0")
	if err != nil || version != "1.0.0" {
		t.Fatalf("ensureBootstrap() payload=%q version=%q error=%v", payload, version, err)
	}
	paths, bundleVersion, err := ActiveBundle(launcherPath)
	if err != nil || bundleVersion != "1.0.0" || payload != paths.Kado {
		t.Fatalf("ActiveBundle() paths=%#v version=%q error=%v", paths, bundleVersion, err)
	}
	assertContent(t, paths.Kado, []byte("stable-kado"))
	assertContent(t, paths.A2A, []byte("stable-a2a"))
}

func bundleFixture(label string) ExecutableBundle {
	return ExecutableBundle{Kado: []byte("kado-" + label), A2A: []byte("a2a-" + label)}
}

func installBundleForTest(t *testing.T, launcherPath, version string, bundle ExecutableBundle) {
	t.Helper()
	if err := WithUpdateLock(launcherPath, func() error {
		return InstallBundleVersionLocked(launcherPath, version, bundle)
	}); err != nil {
		t.Fatalf("install bundle %s: %v", version, err)
	}
}

func activationCount(t *testing.T, launcherPath string) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(managedRoot(launcherPath), "activations"))
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, entry := range entries {
		if activationPattern.MatchString(entry.Name()) {
			count++
		}
	}
	return count
}

func newestActivation(t *testing.T, launcherPath string) activation {
	t.Helper()
	directory := filepath.Join(managedRoot(launcherPath), "activations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatal(err)
	}
	newest := ""
	for _, entry := range entries {
		if activationPattern.MatchString(entry.Name()) && entry.Name() > newest {
			newest = entry.Name()
		}
	}
	if newest == "" {
		t.Fatal("activation missing")
	}
	record, err := readActivation(directory, newest)
	if err != nil {
		t.Fatal(err)
	}
	return record
}
