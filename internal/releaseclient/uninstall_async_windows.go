package releaseclient

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/kado-so/search/internal/buildinfo"
	"github.com/kado-so/search/internal/launcher"
	"github.com/kado-so/search/internal/payload"
	"golang.org/x/sys/windows"
)

func StartCompleteUninstall(target string, info buildinfo.Info) error {
	if info.InstallChannel != "direct" || info.MCP == nil {
		return ErrUninstall
	}
	key, err := ParsePublicKey(info.ReleasePublicKey)
	if err != nil {
		return err
	}
	active, err := launcher.ActiveComplete(target, key)
	if err != nil {
		return err
	}
	value, err := payload.ReadFile(active.Root, active.Manifest.Entries["kado"], payload.MaxFile)
	if err != nil {
		return err
	}
	temporary, err := os.MkdirTemp(filepath.Dir(target), ".kado-uninstall-helper-")
	if err != nil {
		return err
	}
	self := filepath.Join(temporary, "kado.exe")
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(temporary)
		}
	}()
	if err := os.WriteFile(self, value, 0700); err != nil {
		return err
	}
	result := temporary + ".gen.json"
	var handles []syscall.Handle
	var waits []string
	for _, pid := range []int{os.Getpid(), os.Getppid()} {
		h, e := windows.OpenProcess(windows.SYNCHRONIZE|windows.PROCESS_QUERY_LIMITED_INFORMATION, true, uint32(pid))
		if e != nil {
			if pid == os.Getpid() {
				return ErrUninstall
			}
			continue
		}
		if pid != os.Getpid() {
			buffer := make([]uint16, 32768)
			length := uint32(len(buffer))
			if windows.QueryFullProcessImageName(h, 0, &buffer[0], &length) != nil || !strings.EqualFold(windows.UTF16ToString(buffer[:length]), target) {
				windows.CloseHandle(h)
				continue
			}
		}
		defer windows.CloseHandle(h)
		handles = append(handles, syscall.Handle(h))
		waits = append(waits, strconv.FormatUint(uint64(h), 10))
	}
	c := exec.Command(self, "__uninstall-bundle", "--target", target, "--wait-handles", strings.Join(waits, ","))
	c.Env = payload.NodeEnvironment(os.Environ())
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP, AdditionalInheritedHandles: handles}
	if err := c.Start(); err != nil {
		return err
	}
	keep = true
	_ = c.Process.Release()
	return &PendingUninstall{ResultPath: result}
}

func RunCompleteUninstallHelper(args []string, info buildinfo.Info) error {
	if len(args) != 4 || args[0] != "--target" || args[2] != "--wait-handles" || info.InstallChannel != "direct" || info.MCP == nil {
		return ErrUninstall
	}
	target := args[1]
	self, err := os.Executable()
	if err != nil {
		return err
	}
	dir := filepath.Dir(self)
	if !filepath.IsAbs(target) || filepath.Base(target) != "kado.exe" || filepath.Dir(dir) != filepath.Dir(target) || !strings.HasPrefix(filepath.Base(dir), ".kado-uninstall-helper-") || payload.PlainPath(dir) != nil {
		return ErrUninstall
	}
	result := dir + ".gen.json"
	finish := func(err error) error {
		status := struct {
			Schema  string `json:"schema"`
			Status  string `json:"status"`
			Message string `json:"message"`
		}{"kado.uninstall.v1", "success", "Kado removed"}
		if err != nil {
			status.Status, status.Message = "failure", "Kado removal failed or is busy"
		}
		b, _ := json.Marshal(status)
		// Readers must see either no status or the complete durable document.
		temporary := filepath.Join(dir, "result.tmp")
		f, writeErr := os.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if writeErr == nil {
			_, writeErr = f.Write(append(b, '\n'))
			if writeErr == nil {
				writeErr = f.Sync()
			}
			if closeErr := f.Close(); writeErr == nil {
				writeErr = closeErr
			}
			if writeErr == nil {
				from, _ := windows.UTF16PtrFromString(temporary)
				to, _ := windows.UTF16PtrFromString(result)
				writeErr = windows.MoveFileEx(from, to, windows.MOVEFILE_WRITE_THROUGH)
			}
		}
		_ = os.Remove(temporary)
		// Remove only our exact helper executable after its image handle closes.
		quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }
		script := fmt.Sprintf("$p=%s; $d=%s; for($i=0;$i -lt 100;$i++){try {Remove-Item -LiteralPath $p -Force -ErrorAction Stop; Remove-Item -LiteralPath $d -ErrorAction Stop; break} catch {Start-Sleep -Milliseconds 100}}", quote(self), quote(dir))
		if system, systemErr := windows.GetSystemDirectory(); systemErr == nil {
			cleanup := exec.Command(filepath.Join(system, "WindowsPowerShell", "v1.0", "powershell.exe"), "-NoProfile", "-NonInteractive", "-Command", script)
			cleanup.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: windows.DETACHED_PROCESS | windows.CREATE_NEW_PROCESS_GROUP}
			if cleanup.Start() == nil {
				_ = cleanup.Process.Release()
			}
		}
		if writeErr != nil {
			return writeErr
		}
		return err
	}
	waits := strings.Split(args[3], ",")
	if len(waits) == 0 || len(waits) > 2 {
		return finish(ErrUninstall)
	}
	for _, raw := range waits {
		n, e := strconv.ParseUint(raw, 10, 64)
		if e != nil || n == 0 {
			return finish(ErrUninstall)
		}
		h := windows.Handle(n)
		status, e := windows.WaitForSingleObject(h, 60_000)
		windows.CloseHandle(h)
		if e != nil || status != windows.WAIT_OBJECT_0 {
			return finish(ErrUninstall)
		}
	}
	key, err := ParsePublicKey(info.ReleasePublicKey)
	if err != nil {
		return finish(err)
	}
	return finish(launcher.UninstallComplete(target, key))
}
