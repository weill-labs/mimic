package main

import (
	"bytes"
	"reflect"
	"testing"

	"github.com/weill-labs/mimic/internal/driver"
)

func TestParseCLIArgs(t *testing.T) {
	t.Run("parses socket resume and passthrough args", func(t *testing.T) {
		socketPath, resume, extraArgs, err := parseCLIArgs([]string{
			"--socket", "/tmp/mimic.sock",
			"--resume",
			"--",
			"--model", "gpt-5.4",
		})
		if err != nil {
			t.Fatalf("parseCLIArgs() error = %v", err)
		}
		if socketPath != "/tmp/mimic.sock" {
			t.Fatalf("socketPath = %q, want %q", socketPath, "/tmp/mimic.sock")
		}
		if !resume {
			t.Fatal("resume = false, want true")
		}
		want := []string{"--model", "gpt-5.4"}
		if !reflect.DeepEqual(extraArgs, want) {
			t.Fatalf("extraArgs = %v, want %v", extraArgs, want)
		}
	})

	t.Run("treats resume after separator as passthrough arg", func(t *testing.T) {
		socketPath, resume, extraArgs, err := parseCLIArgs([]string{"--", "--resume"})
		if err != nil {
			t.Fatalf("parseCLIArgs() error = %v", err)
		}
		if socketPath != "" {
			t.Fatalf("socketPath = %q, want empty", socketPath)
		}
		if resume {
			t.Fatal("resume = true, want false")
		}
		want := []string{"--resume"}
		if !reflect.DeepEqual(extraArgs, want) {
			t.Fatalf("extraArgs = %v, want %v", extraArgs, want)
		}
	})
}

func TestParseCLIArgs_MissingSocketPath(t *testing.T) {
	if _, _, _, err := parseCLIArgs([]string{"--socket"}); err == nil {
		t.Fatal("parseCLIArgs() succeeded, want error")
	}
}

type fakeResumeDriver struct {
	states []driver.State
	resume driver.Submission
}

func (f *fakeResumeDriver) Name() string { return "fake" }

func (f *fakeResumeDriver) DetectState(driver.Screen) driver.State {
	if len(f.states) == 0 {
		return driver.StateIdle
	}
	state := f.states[0]
	if len(f.states) > 1 {
		f.states = f.states[1:]
	}
	return state
}

func (f *fakeResumeDriver) SubmitPrompt(string) driver.Submission { return driver.Submission{} }
func (f *fakeResumeDriver) ResumePrompt() driver.Submission       { return f.resume }
func (f *fakeResumeDriver) CancelWork() []byte                    { return nil }

type fakeScreen struct{}

func (fakeScreen) Render() string       { return "" }
func (fakeScreen) Contains(string) bool { return false }
func (fakeScreen) Line(int) string      { return "" }
func (fakeScreen) Width() int           { return 80 }
func (fakeScreen) Height() int          { return 24 }

func TestRunResumeFlow(t *testing.T) {
	t.Run("writes resume selection once picker is idle", func(t *testing.T) {
		d := &fakeResumeDriver{
			states: []driver.State{driver.StateIdle},
			resume: driver.Submission{
				Body:   []byte{'.'},
				Submit: []byte{'\r'},
			},
		}
		var out bytes.Buffer
		if err := runResumeFlow(d, fakeScreen{}, &out); err != nil {
			t.Fatalf("runResumeFlow() error = %v", err)
		}
		if got := out.String(); got != ".\r" {
			t.Fatalf("written bytes = %q, want %q", got, ".\r")
		}
	})

	t.Run("dismisses trust prompt before selecting session", func(t *testing.T) {
		d := &fakeResumeDriver{
			states: []driver.State{driver.StateTrustPrompt, driver.StateIdle},
			resume: driver.Submission{
				Body:   []byte{'.'},
				Submit: []byte{'\r'},
			},
		}
		var out bytes.Buffer
		if err := runResumeFlow(d, fakeScreen{}, &out); err != nil {
			t.Fatalf("runResumeFlow() error = %v", err)
		}
		if got := out.String(); got != "\r.\r" {
			t.Fatalf("written bytes = %q, want %q", got, "\r.\r")
		}
	})

	t.Run("rejects unsupported resume flow", func(t *testing.T) {
		d := &fakeResumeDriver{states: []driver.State{driver.StateIdle}}
		if err := runResumeFlow(d, fakeScreen{}, &bytes.Buffer{}); err == nil {
			t.Fatal("runResumeFlow() succeeded, want error")
		}
	})
}
