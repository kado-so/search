package launcher

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/kado-so/search/internal/payload"
)

type payloadReference struct {
	IPCVersion       int    `json:"ipcVersion"`
	InstanceID       string `json:"instanceId"`
	SessionName      string `json:"sessionName"`
	PID              int    `json:"pid"`
	HostPID          int    `json:"hostPid"`
	SocketPath       string `json:"socketPath"`
	Binding          string `json:"binding"`
	ComponentVersion string `json:"componentVersion"`
	InstallationID   string `json:"installationId"`
	ComponentRoot    string `json:"componentRoot"`
	Runtime          string `json:"runtime"`
	CreatedAt        string `json:"createdAt"`
}

func cleanNodeEnvironment(values []string) []string {
	result := []string{}
	for _, v := range values {
		k := strings.ToUpper(strings.SplitN(v, "=", 2)[0])
		if strings.HasPrefix(k, "NODE_") || strings.HasPrefix(k, "NAPI_") || strings.HasPrefix(k, "BUN_") || strings.HasPrefix(k, "LD_") || strings.HasPrefix(k, "DYLD_") || strings.HasPrefix(k, "NPM_CONFIG_") || k == "OPENSSL_CONF" || k == "OPENSSL_MODULES" {
			continue
		}
		result = append(result, v)
	}
	return result
}

func withPayloadMaintenance(root string, runner CompletePaths, key ed25519.PublicKey, closeSessions bool, action func(map[string]bool, func() error) error) error {
	if _, err := payload.VerifyTree(runner.Root, runner.Digest, key); err != nil {
		return err
	}
	versions := filepath.Join(root, "versions")
	entries, err := os.ReadDir(versions)
	if err != nil {
		return err
	}
	// Only roots computed from this installation's immutable version inventory
	// can become close targets. State files never nominate executable paths.
	allowed := map[string]string{}
	var roots []string
	for _, e := range entries {
		if !payload.ValidVersion(e.Name()) {
			continue
		}
		versionRoot := filepath.Join(versions, e.Name())
		if err := payload.PlainPath(versionRoot); err != nil {
			return ErrBusy
		}
		canonicalRoot, err := filepath.EvalSymlinks(versionRoot)
		if err != nil {
			return ErrBusy
		}
		component := filepath.Join(canonicalRoot, "mcp", "app")
		if err := payload.PlainPath(component); err != nil && !os.IsNotExist(err) {
			return ErrBusy
		}
		allowed[component] = versionRoot
		roots = append(roots, component)
	}
	operation := "hold"
	if closeSessions {
		operation = "close-hold"
	}
	request, _ := json.Marshal(struct {
		Schema    int      `json:"schema"`
		Operation string   `json:"operation"`
		Roots     []string `json:"roots"`
	}{1, operation, roots})
	// Empty inventory still needs the shared gate: a new home may register while
	// update starts. A default-home shortcut would reintroduce that race.
	if roots == nil {
		request = []byte("{\"schema\":1,\"operation\":\"" + operation + "\",\"roots\":[]}")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, runner.Entry("node"), "--no-global-search-paths", runner.Entry("maintenance"), "--installation", root)
	cmd.Env = cleanNodeEnvironment(os.Environ())
	cmd.Dir = runner.Root
	configureMaintenanceProcess(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// Errors are deliberately bounded and generic; no session/provider details
	// are copied into release diagnostics.
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}
	finished := make(chan error, 1)
	go func() { finished <- cmd.Wait() }()
	waited := false
	defer func() {
		_ = stdin.Close()
		cancel()
		if !waited {
			<-finished
		}
	}()
	written := make(chan error, 1)
	go func() { _, err := stdin.Write(append(request, '\n')); written <- err }()
	select {
	case err := <-written:
		if err != nil {
			return ErrBusy
		}
	case <-time.After(30 * time.Second):
		return ErrBusy
	case <-finished:
		waited = true
		return ErrBusy
	}
	type ready struct {
		line []byte
		err  error
	}
	response := make(chan ready, 1)
	go func() {
		r := bufio.NewReader(io.LimitReader(stdout, 4<<20))
		b, err := r.ReadBytes('\n')
		response <- ready{b, err}
	}()
	var line []byte
	select {
	case r := <-response:
		if r.err != nil {
			return ErrBusy
		}
		line = r.line
	case <-time.After(90 * time.Second):
		return ErrBusy
	case <-finished:
		waited = true
		return ErrBusy
	}
	var state struct {
		Schema     int                `json:"schema"`
		References []payloadReference `json:"references"`
	}
	d := json.NewDecoder(bytes.NewReader(line))
	d.DisallowUnknownFields()
	if d.Decode(&state) != nil || d.Decode(new(any)) != io.EOF || state.Schema != 1 {
		return ErrBusy
	}
	refs := map[string]bool{}
	for _, ref := range state.References {
		p, ours := allowed[ref.ComponentRoot]
		if !ours {
			// References to a different installation remain outside this operation.
			// Ambiguous paths into this installation must block pruning.
			rel, err := filepath.Rel(root, ref.ComponentRoot)
			if err != nil || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))) {
				return ErrBusy
			}
			continue
		}
		wantRuntime := filepath.Join(p, filepath.FromSlash(runner.Manifest.Entries["node"]))
		if ref.IPCVersion != 1 || ref.InstallationID != payload.Digest([]byte(ref.ComponentRoot)) || ref.Runtime != wantRuntime {
			return ErrBusy
		}
		refs[p] = true
	}
	if closeSessions && len(refs) != 0 {
		return ErrBusy
	}
	alive := func() error {
		if waited {
			return ErrBusy
		}
		select {
		case <-finished:
			waited = true
			return ErrBusy
		default:
			return nil
		}
	}
	if err := alive(); err != nil {
		return err
	}
	if err := action(refs, alive); err != nil {
		return err
	}
	if _, err := stdin.Write([]byte("release\n")); err != nil {
		return ErrBusy
	}
	_ = stdin.Close()
	select {
	case err := <-finished:
		waited = true
		if err != nil {
			return ErrBusy
		}
	case <-time.After(10 * time.Second):
		return ErrBusy
	}
	return nil
}
