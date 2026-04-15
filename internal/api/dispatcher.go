package api

import (
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/weill-labs/mimic/internal/driver"
)

// WireState is the state vocabulary exposed on the Unix socket.
type WireState string

const (
	WireStateStarting WireState = "starting"
	WireStateIdle     WireState = "idle"
	WireStateWorking  WireState = "working"
	WireStateComplete WireState = "complete"
	WireStateError    WireState = "error"
	WireStateExited   WireState = "exited"
)

const (
	defaultPollInterval         = 100 * time.Millisecond
	defaultTrustDismissInterval = 500 * time.Millisecond
	defaultTrustDismissMax      = 5
	defaultSubmitGrace          = 2 * time.Second
)

var errSubmissionCanceled = errors.New("submission canceled")

// Status is the dispatcher snapshot exposed by the status RPC.
type Status struct {
	State WireState `json:"state"`
}

// Dispatcher bridges driver state detection, PTY writes, and the wire-state
// machine used by the Unix socket API.
type Dispatcher struct {
	driver driver.Driver
	screen driver.Screen
	ptmx   io.Writer

	writeMu sync.Mutex

	mu                sync.Mutex
	currentState      driver.State
	completeLatched   bool
	submitInProgress  bool
	submitCanceled    bool
	submitBlockUntil  time.Time
	lastTrustDismiss  time.Time
	trustDismissCount int
	trustDismissStuck bool

	status atomic.Value

	stopOnce sync.Once
	stopCh   chan struct{}
	doneCh   chan struct{}

	pollInterval         time.Duration
	trustDismissInterval time.Duration
	trustDismissMax      int
	submitGrace          time.Duration
}

// NewDispatcher constructs and starts the state-polling dispatcher.
func NewDispatcher(d driver.Driver, screen driver.Screen, ptmx io.Writer) *Dispatcher {
	return newDispatcher(
		d,
		screen,
		ptmx,
		defaultPollInterval,
		defaultTrustDismissInterval,
		defaultTrustDismissMax,
		defaultSubmitGrace,
	)
}

func newDispatcher(
	d driver.Driver,
	screen driver.Screen,
	ptmx io.Writer,
	pollInterval time.Duration,
	trustDismissInterval time.Duration,
	trustDismissMax int,
	submitGrace time.Duration,
) *Dispatcher {
	if trustDismissMax <= 0 {
		trustDismissMax = 1
	}

	dispatcher := &Dispatcher{
		driver:               d,
		screen:               screen,
		ptmx:                 ptmx,
		currentState:         driver.StateStarting,
		stopCh:               make(chan struct{}),
		doneCh:               make(chan struct{}),
		pollInterval:         pollInterval,
		trustDismissInterval: trustDismissInterval,
		trustDismissMax:      trustDismissMax,
		submitGrace:          submitGrace,
	}
	dispatcher.status.Store(Status{State: WireStateStarting})
	go dispatcher.pollLoop()
	return dispatcher
}

// Close stops the background polling loop.
func (d *Dispatcher) Close() {
	d.stopOnce.Do(func() {
		close(d.stopCh)
		<-d.doneCh
	})
}

// Status returns the latest wire-state snapshot without touching the PTY.
func (d *Dispatcher) Status() Status {
	return d.status.Load().(Status)
}

// Submit types and submits a prompt according to the driver's pacing hints.
// It rejects unless the wire state is idle or complete.
func (d *Dispatcher) Submit(prompt string) error {
	submission := d.driver.SubmitPrompt(prompt)
	if len(submission.Body) == 0 && len(submission.Submit) == 0 {
		return errors.New("submit rejected: empty prompt")
	}

	now := time.Now()

	d.mu.Lock()
	wireState := d.wireStateLocked()
	if d.submitInProgress || (!d.submitBlockUntil.IsZero() && now.Before(d.submitBlockUntil)) {
		d.mu.Unlock()
		return errors.New("submit rejected: submission already in progress")
	}
	if wireState != WireStateIdle && wireState != WireStateComplete {
		d.mu.Unlock()
		return fmt.Errorf("submit rejected: current state is %s", wireState)
	}

	d.completeLatched = false
	d.submitInProgress = true
	d.submitCanceled = false
	d.submitBlockUntil = time.Time{}
	d.storeStatusLocked(mapWireState(d.currentState, d.completeLatched))
	d.mu.Unlock()

	err := d.typeSubmission(submission)

	d.mu.Lock()
	d.submitInProgress = false
	if err == nil && !d.submitCanceled {
		d.submitBlockUntil = time.Now().Add(d.submitGrace)
	} else {
		d.submitBlockUntil = time.Time{}
	}
	d.submitCanceled = false
	d.storeStatusLocked(mapWireState(d.currentState, d.completeLatched))
	d.mu.Unlock()

	if err != nil {
		if errors.Is(err, errSubmissionCanceled) {
			return errors.New("submit canceled")
		}
		return err
	}
	return nil
}

// Cancel writes the driver's cancel key sequence immediately.
func (d *Dispatcher) Cancel() error {
	d.mu.Lock()
	if d.submitInProgress {
		d.submitCanceled = true
	}
	d.mu.Unlock()

	return d.writeAll(d.driver.CancelWork())
}

func (d *Dispatcher) pollLoop() {
	defer close(d.doneCh)

	d.refresh()

	ticker := time.NewTicker(d.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-d.stopCh:
			return
		case <-ticker.C:
			d.refresh()
		}
	}
}

func (d *Dispatcher) refresh() {
	rawState := d.driver.DetectState(d.screen)
	now := time.Now()

	shouldDismissTrust := false

	d.mu.Lock()
	if rawState == driver.StateTrustPrompt {
		d.currentState = rawState
		if d.trustDismissCount >= d.trustDismissMax {
			d.trustDismissStuck = true
			d.storeStatusLocked(d.wireStateLocked())
			d.mu.Unlock()
			return
		}
		if now.Sub(d.lastTrustDismiss) >= d.trustDismissInterval {
			d.lastTrustDismiss = now
			d.trustDismissCount++
			shouldDismissTrust = true
		}
		d.storeStatusLocked(d.wireStateLocked())
		d.mu.Unlock()

		if shouldDismissTrust {
			_ = d.writeAll([]byte{'\r'})
		}
		return
	}

	d.trustDismissCount = 0
	d.trustDismissStuck = false
	d.lastTrustDismiss = time.Time{}
	if d.currentState == driver.StateWorking && (rawState == driver.StateIdle || rawState == driver.StateError) {
		d.completeLatched = true
	}
	if rawState == driver.StateError {
		d.completeLatched = true
	}
	if !d.submitBlockUntil.IsZero() {
		switch rawState {
		case driver.StateWorking, driver.StateError, driver.StateExited:
			d.submitBlockUntil = time.Time{}
		default:
			if now.After(d.submitBlockUntil) {
				// Fast turns can finish between poll ticks; once the grace window
				// expires on an idle screen, treat the accepted submit as complete.
				if rawState == driver.StateIdle {
					d.completeLatched = true
				}
				d.submitBlockUntil = time.Time{}
			}
		}
	}

	d.currentState = rawState
	d.storeStatusLocked(d.wireStateLocked())
	d.mu.Unlock()
}

func (d *Dispatcher) typeSubmission(submission driver.Submission) error {
	for i, b := range submission.Body {
		if d.isSubmitCanceled() {
			return errSubmissionCanceled
		}
		if err := d.writeAll([]byte{b}); err != nil {
			return err
		}
		if i == len(submission.Body)-1 || submission.KeyDelay <= 0 {
			continue
		}
		time.Sleep(submission.KeyDelay)
	}

	if d.isSubmitCanceled() {
		return errSubmissionCanceled
	}
	if submission.SettleDelay > 0 {
		time.Sleep(submission.SettleDelay)
	}
	if d.isSubmitCanceled() {
		return errSubmissionCanceled
	}
	return d.writeAll(submission.Submit)
}

func (d *Dispatcher) isSubmitCanceled() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.submitCanceled
}

func (d *Dispatcher) writeAll(data []byte) error {
	if len(data) == 0 {
		return nil
	}

	d.writeMu.Lock()
	defer d.writeMu.Unlock()

	for len(data) > 0 {
		n, err := d.ptmx.Write(data)
		if n > 0 {
			data = data[n:]
		}
		if err != nil {
			return err
		}
		if n == 0 {
			return io.ErrShortWrite
		}
	}
	return nil
}

func (d *Dispatcher) storeStatusLocked(state WireState) {
	d.status.Store(Status{State: state})
}

func (d *Dispatcher) wireStateLocked() WireState {
	if d.trustDismissStuck {
		return WireStateError
	}
	if d.submitInProgress {
		return WireStateWorking
	}
	// Surface accepted submits as working until either a real working screen
	// arrives or the grace window expires.
	if !d.submitBlockUntil.IsZero() && !d.completeLatched && d.currentState != driver.StateWorking && d.currentState != driver.StateExited {
		return WireStateWorking
	}
	return mapWireState(d.currentState, d.completeLatched)
}

func mapWireState(rawState driver.State, completeLatched bool) WireState {
	switch rawState {
	case driver.StateIdle:
		if completeLatched {
			return WireStateComplete
		}
		return WireStateIdle
	case driver.StateWorking:
		return WireStateWorking
	case driver.StateError:
		return WireStateComplete
	case driver.StateExited:
		return WireStateExited
	default:
		return WireStateStarting
	}
}
