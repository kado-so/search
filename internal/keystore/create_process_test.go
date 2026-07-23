package keystore

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	keyring "github.com/zalando/go-keyring"
)

const createProcessHelperEnvironment = "KADO_KEYSTORE_CREATE_HELPER"
const conditionalDeleteProcessHelperEnvironment = "KADO_KEYSTORE_CONDITIONAL_DELETE_HELPER"

func TestCreateRetainsOneWinnerAcrossProcesses(t *testing.T) {
	kinds := []string{"keychain"}
	if runtime.GOOS != "windows" {
		kinds = append(kinds, "file")
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			root := resolvedTempDir(t)
			private := filepath.Join(root, "private")
			if err := os.Mkdir(private, 0o700); err != nil {
				t.Fatalf("Mkdir(private) error = %v", err)
			}
			start := filepath.Join(root, "start")
			const processCount = 6
			commands := make([]*exec.Cmd, 0, processCount)
			var outputs [processCount]bytes.Buffer
			for index := range processCount {
				command := exec.Command(
					os.Args[0],
					"-test.run=^TestCreateProcessHelper$",
				)
				command.Env = append(
					os.Environ(),
					createProcessHelperEnvironment+"=1",
					"KADO_KEYSTORE_CREATE_KIND="+kind,
					"KADO_KEYSTORE_CREATE_ROOT="+root,
					"KADO_KEYSTORE_CREATE_START="+start,
					"KADO_KEYSTORE_CREATE_ID="+integerText(index),
				)
				command.Stdout = &outputs[index]
				command.Stderr = &outputs[index]
				if err := command.Start(); err != nil {
					t.Fatalf("Start(process %d) error = %v", index, err)
				}
				commands = append(commands, command)
			}
			if err := os.WriteFile(start, []byte("start"), 0o600); err != nil {
				t.Fatalf("WriteFile(start) error = %v", err)
			}
			for index, command := range commands {
				if err := command.Wait(); err != nil {
					t.Fatalf(
						"Wait(process %d) error = %v; output = %s",
						index,
						err,
						outputs[index].String(),
					)
				}
			}
			var winner []byte
			for index := range processCount {
				encoded, err := os.ReadFile(filepath.Join(root, "result-"+integerText(index)))
				if err != nil {
					t.Fatalf("ReadFile(result %d) error = %v", index, err)
				}
				if index == 0 {
					winner = encoded
					continue
				}
				if !bytes.Equal(encoded, winner) {
					t.Fatalf("process %d retained a different identity", index)
				}
			}
		})
	}
}

func TestCreateProcessHelper(t *testing.T) {
	if os.Getenv(createProcessHelperEnvironment) != "1" {
		return
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(os.Getenv("KADO_KEYSTORE_CREATE_START")); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Stat(start) error = %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for process start barrier")
		}
		time.Sleep(time.Millisecond)
	}

	root := os.Getenv("KADO_KEYSTORE_CREATE_ROOT")
	identifier := os.Getenv("KADO_KEYSTORE_CREATE_ID")
	var store Store
	switch os.Getenv("KADO_KEYSTORE_CREATE_KIND") {
	case "file":
		configured, err := NewFileStore(
			filepath.Join(root, "private", "management-key.json"),
		)
		if err != nil {
			t.Fatalf("NewFileStore() error = %v", err)
		}
		store = configured
	case "keychain":
		configured := newOSKeychainStore(processFileKeychainBackend{
			path: filepath.Join(root, "keychain-record"),
		})
		configured.service = "kado.test." + filepath.Base(root)
		store = configured
	default:
		t.Fatal("unknown helper store kind")
	}
	winning, _, err := store.Create([]byte("candidate-" + identifier))
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(root, "result-"+identifier),
		winning,
		0o600,
	); err != nil {
		t.Fatalf("WriteFile(result) error = %v", err)
	}
}

func TestConditionalDeleteNeverDeletesReenrolledKeyAcrossProcesses(t *testing.T) {
	kinds := []string{"keychain"}
	if runtime.GOOS != "windows" {
		kinds = append(kinds, "file")
	}
	for _, kind := range kinds {
		t.Run(kind, func(t *testing.T) {
			root := resolvedTempDir(t)
			private := filepath.Join(root, "private")
			if err := os.Mkdir(private, 0o700); err != nil {
				t.Fatalf("Mkdir(private) error = %v", err)
			}
			store := processTestStore(t, kind, root)
			oldKey := []byte("management-key-A")
			newKey := []byte("management-key-B")
			if err := store.Save(oldKey); err != nil {
				t.Fatalf("Save(old key) error = %v", err)
			}

			allowStale := filepath.Join(root, "allow-stale-delete")
			replaced := filepath.Join(root, "replacement-complete")
			stale := conditionalDeleteHelperCommand(
				kind,
				root,
				"stale-delete",
				allowStale,
				replaced,
			)
			replacer := conditionalDeleteHelperCommand(
				kind,
				root,
				"replace",
				allowStale,
				replaced,
			)
			var staleOutput, replacerOutput bytes.Buffer
			stale.Stdout, stale.Stderr = &staleOutput, &staleOutput
			replacer.Stdout, replacer.Stderr = &replacerOutput, &replacerOutput
			if err := stale.Start(); err != nil {
				t.Fatalf("Start(stale delete) error = %v", err)
			}
			if err := replacer.Start(); err != nil {
				t.Fatalf("Start(replacer) error = %v", err)
			}
			waitForProcessMarker(t, replaced)
			if err := os.WriteFile(allowStale, []byte("continue"), 0o600); err != nil {
				t.Fatalf("WriteFile(allow stale delete) error = %v", err)
			}
			if err := replacer.Wait(); err != nil {
				t.Fatalf("Wait(replacer) error = %v; output = %s", err, replacerOutput.String())
			}
			if err := stale.Wait(); err != nil {
				t.Fatalf("Wait(stale delete) error = %v; output = %s", err, staleOutput.String())
			}
			if !strings.Contains(staleOutput.String(), "retained\n") {
				t.Fatalf("stale delete output = %q", staleOutput.String())
			}
			current, err := store.Load()
			if err != nil {
				t.Fatalf("Load(final key) error = %v", err)
			}
			if !bytes.Equal(current, newKey) {
				t.Fatalf("stale revocation changed re-enrolled key: %q", current)
			}
		})
	}
}

func TestConditionalDeleteProcessHelper(t *testing.T) {
	if os.Getenv(conditionalDeleteProcessHelperEnvironment) != "1" {
		return
	}
	kind := os.Getenv("KADO_KEYSTORE_RACE_KIND")
	root := os.Getenv("KADO_KEYSTORE_RACE_ROOT")
	store := processTestStore(t, kind, root)
	oldKey := []byte("management-key-A")
	switch os.Getenv("KADO_KEYSTORE_RACE_OPERATION") {
	case "replace":
		deleted, err := store.DeleteIfMatches(oldKey)
		if err != nil || !deleted {
			t.Fatalf("DeleteIfMatches(old key) deleted=%t error=%v", deleted, err)
		}
		winning, created, err := store.Create([]byte("management-key-B"))
		if err != nil || !created || !bytes.Equal(winning, []byte("management-key-B")) {
			t.Fatalf("Create(new key) winning=%q created=%t error=%v", winning, created, err)
		}
		if err := os.WriteFile(
			os.Getenv("KADO_KEYSTORE_RACE_REPLACED"),
			[]byte("done"),
			0o600,
		); err != nil {
			t.Fatalf("WriteFile(replaced) error = %v", err)
		}
	case "stale-delete":
		waitForProcessMarker(t, os.Getenv("KADO_KEYSTORE_RACE_ALLOW_STALE"))
		deleted, err := store.DeleteIfMatches(oldKey)
		if err != nil {
			t.Fatalf("DeleteIfMatches(stale key) error = %v", err)
		}
		if deleted {
			t.Fatal("DeleteIfMatches(stale key) deleted replacement")
		}
		_, _ = os.Stdout.WriteString("retained\n")
	default:
		t.Fatal("unknown conditional-delete helper operation")
	}
}

func conditionalDeleteHelperCommand(
	kind,
	root,
	operation,
	allowStale,
	replaced string,
) *exec.Cmd {
	command := exec.Command(os.Args[0], "-test.run=^TestConditionalDeleteProcessHelper$")
	command.Env = append(
		os.Environ(),
		conditionalDeleteProcessHelperEnvironment+"=1",
		"KADO_KEYSTORE_RACE_KIND="+kind,
		"KADO_KEYSTORE_RACE_ROOT="+root,
		"KADO_KEYSTORE_RACE_OPERATION="+operation,
		"KADO_KEYSTORE_RACE_ALLOW_STALE="+allowStale,
		"KADO_KEYSTORE_RACE_REPLACED="+replaced,
	)
	return command
}

func processTestStore(t *testing.T, kind, root string) Store {
	t.Helper()
	switch kind {
	case "file":
		configured, err := NewFileStore(
			filepath.Join(root, "private", "management-key.json"),
		)
		if err != nil {
			t.Fatalf("NewFileStore() error = %v", err)
		}
		return configured
	case "keychain":
		configured := newOSKeychainStore(processFileKeychainBackend{
			path: filepath.Join(root, "keychain-record"),
		})
		configured.service = "kado.test." + filepath.Base(root)
		return configured
	default:
		t.Fatal("unknown helper store kind")
		return nil
	}
}

func waitForProcessMarker(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("Stat(process marker) error = %v", err)
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for process marker %q", filepath.Base(path))
		}
		time.Sleep(time.Millisecond)
	}
}

type processFileKeychainBackend struct {
	path string
}

func (backend processFileKeychainBackend) Get(_, _ string) (string, error) {
	value, err := os.ReadFile(backend.path)
	if errors.Is(err, os.ErrNotExist) {
		return "", keyring.ErrNotFound
	}
	return string(value), err
}

func (backend processFileKeychainBackend) Set(_, _, value string) error {
	time.Sleep(20 * time.Millisecond)
	return os.WriteFile(backend.path, []byte(value), 0o600)
}

func (backend processFileKeychainBackend) Delete(_, _ string) error {
	if err := os.Remove(backend.path); errors.Is(err, os.ErrNotExist) {
		return keyring.ErrNotFound
	} else {
		return err
	}
}

func integerText(value int) string {
	const digits = "0123456789"
	if value == 0 {
		return "0"
	}
	var buffer [20]byte
	position := len(buffer)
	for value > 0 {
		position--
		buffer[position] = digits[value%10]
		value /= 10
	}
	return string(buffer[position:])
}
