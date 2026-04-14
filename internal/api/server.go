package api

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
)

// Handler is the dispatcher surface the Unix socket server depends on.
type Handler interface {
	Submit(prompt string) error
	Status() Status
	Cancel() error
}

// Server serves the line-delimited JSON Unix socket protocol.
type Server struct {
	path    string
	handler Handler
	ln      net.Listener

	connMu sync.Mutex
	conns  map[net.Conn]struct{}

	wg        sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
}

type request struct {
	Method string         `json:"method"`
	Params *requestParams `json:"params,omitempty"`
}

type requestParams struct {
	Prompt string `json:"prompt,omitempty"`
}

type okResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// NewServer binds the Unix socket and starts serving clients immediately.
func NewServer(path string, handler Handler) (*Server, error) {
	if path == "" {
		return nil, errors.New("socket path is required")
	}
	if err := removeExistingSocket(path); err != nil {
		return nil, err
	}

	ln, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = ln.Close()
		_ = os.Remove(path)
		return nil, err
	}

	server := &Server{
		path:    path,
		handler: handler,
		ln:      ln,
		conns:   map[net.Conn]struct{}{},
	}
	server.wg.Add(1)
	go server.acceptLoop()
	return server, nil
}

// Close stops accepting clients, closes active connections, and removes the
// Unix socket path.
func (s *Server) Close() error {
	s.closeOnce.Do(func() {
		if err := s.ln.Close(); err != nil && !errors.Is(err, net.ErrClosed) {
			s.closeErr = err
		}

		s.connMu.Lock()
		conns := make([]net.Conn, 0, len(s.conns))
		for conn := range s.conns {
			conns = append(conns, conn)
		}
		s.connMu.Unlock()

		for _, conn := range conns {
			_ = conn.Close()
		}

		s.wg.Wait()
		if err := os.Remove(s.path); err != nil && !errors.Is(err, os.ErrNotExist) && s.closeErr == nil {
			s.closeErr = err
		}
	})
	return s.closeErr
}

func (s *Server) acceptLoop() {
	defer s.wg.Done()

	for {
		conn, err := s.ln.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return
			}
			continue
		}

		s.trackConn(conn, true)
		s.wg.Add(1)
		go s.handleConn(conn)
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer s.wg.Done()
	defer func() {
		s.trackConn(conn, false)
		_ = conn.Close()
	}()

	reader := bufio.NewReader(conn)
	writer := bufio.NewWriter(conn)

	for {
		line, err := reader.ReadBytes('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			return
		}

		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			if errors.Is(err, io.EOF) {
				return
			}
			continue
		}

		response := s.handleLine(line)
		if err := writeJSONLine(writer, response); err != nil {
			return
		}

		if errors.Is(err, io.EOF) {
			return
		}
	}
}

func (s *Server) handleLine(line []byte) any {
	var req request
	if err := json.Unmarshal(line, &req); err != nil {
		return okResponse{OK: false, Error: fmt.Sprintf("invalid JSON: %v", err)}
	}

	switch req.Method {
	case "submit":
		prompt := ""
		if req.Params != nil {
			prompt = req.Params.Prompt
		}
		if err := s.handler.Submit(prompt); err != nil {
			return okResponse{OK: false, Error: err.Error()}
		}
		return okResponse{OK: true}
	case "status":
		return s.handler.Status()
	case "cancel":
		if err := s.handler.Cancel(); err != nil {
			return okResponse{OK: false, Error: err.Error()}
		}
		return okResponse{OK: true}
	default:
		return okResponse{OK: false, Error: fmt.Sprintf("unknown method %q", req.Method)}
	}
}

func (s *Server) trackConn(conn net.Conn, add bool) {
	s.connMu.Lock()
	defer s.connMu.Unlock()
	if add {
		s.conns[conn] = struct{}{}
		return
	}
	delete(s.conns, conn)
}

func removeExistingSocket(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSocket == 0 {
		return fmt.Errorf("socket path %q already exists and is not a Unix socket", path)
	}
	return os.Remove(path)
}

func writeJSONLine(w *bufio.Writer, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := w.Write(data); err != nil {
		return err
	}
	if err := w.WriteByte('\n'); err != nil {
		return err
	}
	return w.Flush()
}
