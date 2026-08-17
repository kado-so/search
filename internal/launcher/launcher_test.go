package launcher

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestInstallActivatesImmutableVersionsWithoutReplacingLauncher(t *testing.T) {
	t.Parallel()

	launcherPath := testLauncher(t, []byte("stable-launcher"))
	first := testCandidate(t, []byte("payload-one"))
	second := testCandidate(t, []byte("payload-two"))
	installForTest(t, launcherPath, "1.0.0", first)
	installForTest(t, launcherPath, "1.1.0", second)

	assertContent(t, launcherPath, []byte("stable-launcher"))
	payload, version, err := Active(launcherPath)
	if err != nil || version != "1.1.0" {
		t.Fatalf("Active() payload=%q version=%q error=%v", payload, version, err)
	}
	assertContent(t, payload, []byte("payload-two"))
	assertContent(
		t,
		filepath.Join(managedRoot(launcherPath), "versions", "1.0.0", executableName()),
		[]byte("payload-one"),
	)
	assertContent(t, launcherPath, []byte("stable-launcher"))
}

func TestConcurrentInstallCreatesOneActivation(t *testing.T) {
	t.Parallel()

	launcherPath := testLauncher(t, []byte("stable-launcher"))
	first := testCandidate(t, []byte("payload-one"))
	second := testCandidate(t, []byte("payload-two"))
	installForTest(t, launcherPath, "1.0.0", first)

	const workers = 24
	var wait sync.WaitGroup
	errors := make(chan error, workers)
	for index := 0; index < workers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			errors <- WithUpdateLock(launcherPath, func() error {
				return InstallVersionLocked(launcherPath, "1.1.0", second)
			})
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent install error = %v", err)
		}
	}
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
	if count != 2 {
		t.Fatalf("activation count = %d, want initial plus one update", count)
	}
	_, version, err := Active(launcherPath)
	if err != nil || version != "1.1.0" {
		t.Fatalf("Active() version=%q error=%v", version, err)
	}
}

func TestConcurrentReadersSeeOnlyCompleteOldOrNewPayload(t *testing.T) {
	t.Parallel()

	launcherPath := testLauncher(t, []byte("stable-launcher"))
	firstValue := []byte("payload-one")
	secondValue := []byte("payload-two")
	first := testCandidate(t, firstValue)
	second := testCandidate(t, secondValue)
	installForTest(t, launcherPath, "1.0.0", first)

	const readers = 48
	start := make(chan struct{})
	errors := make(chan error, readers)
	var wait sync.WaitGroup
	for index := 0; index < readers; index++ {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			for attempt := 0; attempt < 50; attempt++ {
				payload, version, err := Active(launcherPath)
				if err != nil {
					errors <- err
					return
				}
				value, err := os.ReadFile(payload)
				if err != nil {
					errors <- err
					return
				}
				if version == "1.0.0" && string(value) == string(firstValue) ||
					version == "1.1.0" && string(value) == string(secondValue) {
					continue
				}
				errors <- fmt.Errorf("version %s had payload %q", version, value)
				return
			}
			errors <- nil
		}()
	}
	close(start)
	installForTest(t, launcherPath, "1.1.0", second)
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestIncompleteOrCorruptNewestActivationFallsBack(t *testing.T) {
	t.Parallel()

	launcherPath := testLauncher(t, []byte("stable-launcher"))
	first := testCandidate(t, []byte("payload-one"))
	installForTest(t, launcherPath, "1.0.0", first)
	directory := filepath.Join(managedRoot(launcherPath), "activations")
	if err := os.WriteFile(filepath.Join(directory, ".kado-activation-interrupted"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("%020d.json", 99)), []byte("{not-json\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	payload, version, err := Active(launcherPath)
	if err != nil || version != "1.0.0" {
		t.Fatalf("Active() payload=%q version=%q error=%v", payload, version, err)
	}
	assertContent(t, payload, []byte("payload-one"))
}

func TestModifiedNewestPayloadFallsBackToVerifiedActivation(t *testing.T) {
	t.Parallel()

	launcherPath := testLauncher(t, []byte("stable-launcher"))
	first := testCandidate(t, []byte("payload-one"))
	second := testCandidate(t, []byte("payload-two"))
	installForTest(t, launcherPath, "1.0.0", first)
	installForTest(t, launcherPath, "1.1.0", second)

	newest := filepath.Join(managedRoot(launcherPath), "versions", "1.1.0", executableName())
	if err := os.WriteFile(newest, []byte("modified-payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	payload, version, err := Active(launcherPath)
	if err != nil || version != "1.0.0" {
		t.Fatalf("Active() payload=%q version=%q error=%v", payload, version, err)
	}
	assertContent(t, payload, []byte("payload-one"))
}

func TestModifiedNewestPayloadCanBeRepaired(t *testing.T) {
	t.Parallel()

	launcherPath := testLauncher(t, []byte("stable-launcher"))
	first := testCandidate(t, []byte("payload-one"))
	secondValue := []byte("payload-two")
	second := testCandidate(t, secondValue)
	installForTest(t, launcherPath, "1.0.0", first)
	installForTest(t, launcherPath, "1.1.0", second)

	newest := filepath.Join(managedRoot(launcherPath), "versions", "1.1.0", executableName())
	if err := os.WriteFile(newest, []byte("modified-payload"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, version, err := Active(launcherPath); err != nil || version != "1.0.0" {
		t.Fatalf("Active() before repair version=%q error=%v", version, err)
	}

	installForTest(t, launcherPath, "1.1.0", second)
	payload, version, err := Active(launcherPath)
	if err != nil || version != "1.1.0" {
		t.Fatalf("Active() after repair payload=%q version=%q error=%v", payload, version, err)
	}
	assertContent(t, payload, secondValue)
}

func TestExternalInstallReceiptDisablesDirectLauncher(t *testing.T) {
	t.Parallel()

	launcherPath := testLauncher(t, []byte("stable-launcher"))
	receiptPath := filepath.Join(filepath.Dir(launcherPath), receiptName)
	if err := os.WriteFile(receiptPath, []byte("{\"schema_version\":1,\"channel\":\"homebrew\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if directInstallation(launcherPath) {
		t.Fatal("external receipt was treated as a direct installation")
	}
	if err := os.WriteFile(receiptPath, []byte("{\"schema_version\":1,\"channel\":\"direct\"}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !directInstallation(launcherPath) {
		t.Fatal("direct receipt was rejected")
	}
}

func installForTest(t *testing.T, launcherPath, version, candidate string) {
	t.Helper()
	if err := WithUpdateLock(launcherPath, func() error {
		return InstallVersionLocked(launcherPath, version, candidate)
	}); err != nil {
		t.Fatalf("install %s: %v", version, err)
	}
}

func testLauncher(t *testing.T, value []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), executableName())
	if err := os.WriteFile(path, value, 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func testCandidate(t *testing.T, value []byte) string {
	t.Helper()
	file, err := os.CreateTemp(t.TempDir(), "candidate-")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Chmod(0o755); err == nil {
		_, err = file.Write(value)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		t.Fatal(err)
	}
	return file.Name()
}

func assertContent(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatalf("%s = %q, want %q", path, got, want)
	}
}
