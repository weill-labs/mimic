//go:build integration

package main_test

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/weill-labs/mimic/internal/api"
)

type okResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func TestIntegration_MimicSocketSubmitAndComplete(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skipf("codex not installed: %v", err)
	}

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "mimic.sock")
	binaryPath := buildMimic(t, tmpDir)
	spawnMimic(t, binaryPath, "codex", "--socket", socketPath)

	waitForSocket(t, socketPath, 10*time.Second)

	if got := waitForWireState(t, socketPath, api.WireStateIdle, 30*time.Second); got != api.WireStateIdle {
		t.Fatalf("mimic did not reach idle: last=%q", got)
	}

	var submitResp okResponse
	rpc(t, socketPath, `{"method":"submit","params":{"prompt":"say hi in one word"}}`, &submitResp)
	if !submitResp.OK {
		t.Fatalf("submit response = %+v, want ok", submitResp)
	}

	if got := waitForWireState(t, socketPath, api.WireStateWorking, 5*time.Second); got != api.WireStateWorking {
		t.Fatalf("mimic did not enter working state: last=%q", got)
	}
	if got := waitForWireState(t, socketPath, api.WireStateComplete, 60*time.Second); got != api.WireStateComplete {
		t.Fatalf("mimic did not enter complete state: last=%q", got)
	}
}

func TestIntegration_MimicSocketCodexCancel(t *testing.T) {
	if _, err := exec.LookPath("codex"); err != nil {
		t.Skipf("codex not installed: %v", err)
	}

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "mimic.sock")
	binaryPath := buildMimic(t, tmpDir)
	spawnMimic(t, binaryPath, "codex", "--socket", socketPath)

	waitForSocket(t, socketPath, 10*time.Second)

	if got := waitForWireState(t, socketPath, api.WireStateIdle, 30*time.Second); got != api.WireStateIdle {
		t.Fatalf("mimic did not reach idle: last=%q", got)
	}

	var submitResp okResponse
	rpc(t, socketPath, `{"method":"submit","params":{"prompt":"write a detailed essay about typewriters"}}`, &submitResp)
	if !submitResp.OK {
		t.Fatalf("submit response = %+v, want ok", submitResp)
	}

	if got := waitForWireState(t, socketPath, api.WireStateWorking, 5*time.Second); got != api.WireStateWorking {
		t.Fatalf("mimic did not enter working state: last=%q", got)
	}

	var cancelResp okResponse
	rpc(t, socketPath, `{"method":"cancel"}`, &cancelResp)
	if !cancelResp.OK {
		t.Fatalf("cancel response = %+v, want ok", cancelResp)
	}

	if got := waitForWireState(t, socketPath, api.WireStateComplete, 60*time.Second); got != api.WireStateComplete {
		t.Fatalf("mimic did not enter complete state after cancel: last=%q", got)
	}
}

func buildMimic(t *testing.T, dir string) string {
	t.Helper()

	binaryPath := filepath.Join(dir, "mimic")
	build := exec.Command("go", "build", "-o", binaryPath, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build mimic: %v\n%s", err, output)
	}
	return binaryPath
}

func spawnMimic(t *testing.T, binaryPath string, args ...string) {
	t.Helper()

	cmd := exec.Command(binaryPath, args...)
	ptmx, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: 100, Rows: 30})
	if err != nil {
		t.Fatalf("start mimic: %v", err)
	}
	t.Cleanup(func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		_ = ptmx.Close()
	})

	go func() {
		_, _ = io.Copy(io.Discard, ptmx)
	}()
}

func waitForSocket(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("socket %q never appeared", path)
}

func waitForWireState(t *testing.T, socketPath string, want api.WireState, timeout time.Duration) api.WireState {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last api.WireState
	for time.Now().Before(deadline) {
		var status api.Status
		rpc(t, socketPath, `{"method":"status"}`, &status)
		last = status.State
		if last == want {
			return last
		}
		time.Sleep(100 * time.Millisecond)
	}
	return last
}

func rpc(t *testing.T, socketPath string, payload string, out any) {
	t.Helper()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial(%q): %v", socketPath, err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(payload + "\n")); err != nil {
		t.Fatalf("Write(%q): %v", payload, err)
	}

	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if err != nil {
		t.Fatalf("ReadBytes(): %v", err)
	}
	if err := json.Unmarshal(line, out); err != nil {
		t.Fatalf("Unmarshal(%q): %v", string(line), err)
	}
}
