package api

import (
	"bufio"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type stubHandler struct {
	mu          sync.Mutex
	status      Status
	submits     []string
	cancelCount int
}

func (h *stubHandler) Submit(prompt string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.submits = append(h.submits, prompt)
	return nil
}

func (h *stubHandler) Status() Status {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.status
}

func (h *stubHandler) Cancel() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.cancelCount++
	return nil
}

func TestServerServesLineDelimitedJSON(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "mimic.sock")
	handler := &stubHandler{status: Status{State: WireStateWorking}}

	server, err := NewServer(socketPath, handler)
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}
	defer server.Close()

	conn, err := net.Dial("unix", socketPath)
	if err != nil {
		t.Fatalf("Dial(): %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte(
		`{"method":"submit","params":{"prompt":"Fix the auth bug"}}` + "\n" +
			`{"method":"status"}` + "\n" +
			`{"method":"cancel"}` + "\n",
	)); err != nil {
		t.Fatalf("Write(): %v", err)
	}

	reader := bufio.NewReader(conn)

	var submitResp okResponse
	readJSONLine(t, reader, &submitResp)
	if !submitResp.OK {
		t.Fatalf("submit response = %+v, want ok", submitResp)
	}

	var statusResp Status
	readJSONLine(t, reader, &statusResp)
	if statusResp.State != WireStateWorking {
		t.Fatalf("status response = %+v, want working", statusResp)
	}

	var cancelResp okResponse
	readJSONLine(t, reader, &cancelResp)
	if !cancelResp.OK {
		t.Fatalf("cancel response = %+v, want ok", cancelResp)
	}

	handler.mu.Lock()
	defer handler.mu.Unlock()
	if len(handler.submits) != 1 || handler.submits[0] != "Fix the auth bug" {
		t.Fatalf("submits = %v, want one prompt", handler.submits)
	}
	if handler.cancelCount != 1 {
		t.Fatalf("cancelCount = %d, want 1", handler.cancelCount)
	}
}

func TestServerCloseRemovesSocket(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "mimic.sock")
	server, err := NewServer(socketPath, &stubHandler{status: Status{State: WireStateIdle}})
	if err != nil {
		t.Fatalf("NewServer(): %v", err)
	}

	if _, err := os.Stat(socketPath); err != nil {
		t.Fatalf("socket stat before close: %v", err)
	}
	if err := server.Close(); err != nil {
		t.Fatalf("Close(): %v", err)
	}
	if _, err := os.Stat(socketPath); !os.IsNotExist(err) {
		t.Fatalf("socket stat after close = %v, want not exist", err)
	}
}

func readJSONLine(t *testing.T, reader *bufio.Reader, out any) {
	t.Helper()
	line, err := reader.ReadBytes('\n')
	if err != nil {
		t.Fatalf("ReadBytes(): %v", err)
	}
	if err := json.Unmarshal(line, out); err != nil {
		t.Fatalf("Unmarshal(%q): %v", string(line), err)
	}
}
