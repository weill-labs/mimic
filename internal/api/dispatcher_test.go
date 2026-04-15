package api

import (
	"bytes"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/weill-labs/mimic/internal/driver"
)

type fakeDriver struct {
	mu          sync.Mutex
	state       driver.State
	keyDelay    time.Duration
	settleDelay time.Duration
	cancel      []byte
}

func (f *fakeDriver) Name() string { return "fake" }

func (f *fakeDriver) DetectState(driver.Screen) driver.State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.state
}

func (f *fakeDriver) SubmitPrompt(prompt string) driver.Submission {
	f.mu.Lock()
	defer f.mu.Unlock()
	if prompt == "" {
		return driver.Submission{}
	}
	return driver.Submission{
		Body:        []byte(prompt),
		Submit:      []byte{'\r'},
		KeyDelay:    f.keyDelay,
		SettleDelay: f.settleDelay,
	}
}

func (f *fakeDriver) ResumePrompt() driver.Submission {
	return driver.Submission{}
}

func (f *fakeDriver) CancelWork() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.cancel...)
}

func (f *fakeDriver) setState(state driver.State) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
}

type fakeScreen struct{}

func (fakeScreen) Render() string       { return "" }
func (fakeScreen) Contains(string) bool { return false }
func (fakeScreen) Line(int) string      { return "" }
func (fakeScreen) Width() int           { return 80 }
func (fakeScreen) Height() int          { return 24 }

type recordingWriter struct {
	mu     sync.Mutex
	buf    bytes.Buffer
	writes chan string
}

func newRecordingWriter() *recordingWriter {
	return &recordingWriter{
		writes: make(chan string, 64),
	}
}

func (w *recordingWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Write(p)
	if w.writes != nil {
		w.writes <- string(append([]byte(nil), p...))
	}
	return len(p), nil
}

func (w *recordingWriter) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func TestDispatcherTracksWireStateAndCompleteLatch(t *testing.T) {
	fake := &fakeDriver{state: driver.StateStarting, cancel: []byte{0x1b}}
	writer := newRecordingWriter()
	dispatcher := newDispatcher(fake, fakeScreen{}, writer, 5*time.Millisecond, time.Hour, 5, 20*time.Millisecond)
	defer dispatcher.Close()

	waitForState(t, dispatcher, WireStateStarting)

	fake.setState(driver.StateIdle)
	waitForState(t, dispatcher, WireStateIdle)

	if err := dispatcher.Submit("first"); err != nil {
		t.Fatalf("Submit(first): %v", err)
	}
	if !strings.Contains(writer.String(), "first\r") {
		t.Fatalf("writer = %q, want prompt bytes", writer.String())
	}

	fake.setState(driver.StateWorking)
	waitForState(t, dispatcher, WireStateWorking)

	fake.setState(driver.StateIdle)
	waitForState(t, dispatcher, WireStateComplete)

	if got := dispatcher.Status().State; got != WireStateComplete {
		t.Fatalf("Status().State = %q, want %q", got, WireStateComplete)
	}

	if err := dispatcher.Submit("second"); err != nil {
		t.Fatalf("Submit(second): %v", err)
	}
	if !strings.Contains(writer.String(), "second\r") {
		t.Fatalf("writer = %q, want second prompt bytes", writer.String())
	}

	fake.setState(driver.StateWorking)
	waitForState(t, dispatcher, WireStateWorking)

	fake.setState(driver.StateError)
	waitForState(t, dispatcher, WireStateComplete)

	fake.setState(driver.StateExited)
	waitForState(t, dispatcher, WireStateExited)
}

func TestDispatcherTracksFastSubmitWithoutObservedWorkingScreen(t *testing.T) {
	t.Parallel()

	fake := &fakeDriver{state: driver.StateIdle, cancel: []byte{0x1b}}
	writer := newRecordingWriter()
	dispatcher := newDispatcher(fake, fakeScreen{}, writer, 5*time.Millisecond, time.Hour, 5, 20*time.Millisecond)
	defer dispatcher.Close()

	waitForState(t, dispatcher, WireStateIdle)

	if err := dispatcher.Submit("fast"); err != nil {
		t.Fatalf("Submit(fast): %v", err)
	}
	if !strings.Contains(writer.String(), "fast\r") {
		t.Fatalf("writer = %q, want prompt bytes", writer.String())
	}

	waitForState(t, dispatcher, WireStateWorking)
	waitForState(t, dispatcher, WireStateComplete)
}

func TestDispatcherRejectsSubmitOutsideIdleOrComplete(t *testing.T) {
	fake := &fakeDriver{state: driver.StateStarting, cancel: []byte{0x1b}}
	dispatcher := newDispatcher(fake, fakeScreen{}, newRecordingWriter(), 5*time.Millisecond, time.Hour, 5, 20*time.Millisecond)
	defer dispatcher.Close()

	if err := dispatcher.Submit("blocked"); err == nil {
		t.Fatal("Submit() succeeded from starting state, want error")
	}

	fake.setState(driver.StateWorking)
	waitForState(t, dispatcher, WireStateWorking)
	if err := dispatcher.Submit("blocked"); err == nil {
		t.Fatal("Submit() succeeded from working state, want error")
	}
}

func TestDispatcherAutoDismissesTrustPromptAndNeverSurfacesIt(t *testing.T) {
	fake := &fakeDriver{state: driver.StateTrustPrompt, cancel: []byte{0x1b}}
	writer := newRecordingWriter()
	dispatcher := newDispatcher(fake, fakeScreen{}, writer, 5*time.Millisecond, 20*time.Millisecond, 5, 20*time.Millisecond)
	defer dispatcher.Close()

	waitForWrite(t, writer, "\r")
	if got := dispatcher.Status().State; got != WireStateStarting {
		t.Fatalf("Status().State = %q, want %q", got, WireStateStarting)
	}

	fake.setState(driver.StateIdle)
	waitForState(t, dispatcher, WireStateIdle)
}

func TestDispatcherTrustPromptRetriesThenErrors(t *testing.T) {
	fake := &fakeDriver{state: driver.StateTrustPrompt, cancel: []byte{0x1b}}
	writer := newRecordingWriter()
	dispatcher := newDispatcher(fake, fakeScreen{}, writer, 5*time.Millisecond, 10*time.Millisecond, 2, 20*time.Millisecond)
	defer dispatcher.Close()

	waitForStringCount(t, writer, "\r", 2)
	waitForState(t, dispatcher, WireStateError)
}

func TestDispatcherCancelInterruptsActiveSubmission(t *testing.T) {
	fake := &fakeDriver{
		state:       driver.StateIdle,
		keyDelay:    25 * time.Millisecond,
		settleDelay: time.Second,
		cancel:      []byte{0x1b},
	}
	writer := newRecordingWriter()
	dispatcher := newDispatcher(fake, fakeScreen{}, writer, 5*time.Millisecond, time.Hour, 5, 50*time.Millisecond)
	defer dispatcher.Close()

	waitForState(t, dispatcher, WireStateIdle)

	errCh := make(chan error, 1)
	go func() {
		errCh <- dispatcher.Submit("abc")
	}()

	waitForWrite(t, writer, "a")
	if err := dispatcher.Cancel(); err != nil {
		t.Fatalf("Cancel(): %v", err)
	}

	err := <-errCh
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("Submit() error = %v, want canceled", err)
	}
	if strings.Contains(writer.String(), "\r") {
		t.Fatalf("writer = %q, want cancel before submit key", writer.String())
	}
	if !strings.Contains(writer.String(), "\x1b") {
		t.Fatalf("writer = %q, want cancel byte", writer.String())
	}
}

func waitForState(t *testing.T, dispatcher *Dispatcher, want WireState) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if got := dispatcher.Status().State; got == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("wire state never became %q; last=%q", want, dispatcher.Status().State)
}

func waitForWrite(t *testing.T, writer *recordingWriter, want string) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case got := <-writer.writes:
			if got == want {
				return
			}
		case <-deadline:
			t.Fatalf("never observed PTY write %q; writes=%q", want, writer.String())
		}
	}
}

func waitForStringCount(t *testing.T, writer *recordingWriter, want string, count int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Count(writer.String(), want) >= count {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("never observed %d writes of %q; writes=%q", count, want, writer.String())
}
