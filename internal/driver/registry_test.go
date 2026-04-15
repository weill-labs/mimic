package driver

import (
	"strings"
	"testing"
)

// fakeDriver is a minimal driver used only by registry tests. It doesn't
// live in a sub-package because the tests need direct access to registry
// internals (they manipulate the package-level map to isolate from real
// driver registrations).
type fakeDriver struct{ name string }

func (f *fakeDriver) Name() string                   { return f.name }
func (f *fakeDriver) DetectState(Screen) State       { return StateUnknown }
func (f *fakeDriver) SubmitPrompt(string) Submission { return Submission{} }
func (f *fakeDriver) ResumePrompt() Submission       { return Submission{} }
func (f *fakeDriver) CancelWork() []byte             { return nil }

// withIsolatedRegistry swaps in a fresh map for the duration of a test and
// restores the original on cleanup, so tests can Register without leaking
// state into other tests (or conflicting with real driver init()).
func withIsolatedRegistry(t *testing.T) {
	t.Helper()
	registryMu.Lock()
	saved := registry
	registry = map[string]registryEntry{}
	registryMu.Unlock()

	t.Cleanup(func() {
		registryMu.Lock()
		registry = saved
		registryMu.Unlock()
	})
}

func TestRegister_AndLookup(t *testing.T) {
	withIsolatedRegistry(t)

	Register(Registration{
		Name:        "fake",
		Binary:      "fakebin",
		DefaultArgs: []string{"--yolo"},
		Factory:     func() Driver { return &fakeDriver{name: "fake"} },
	})

	r, err := Lookup("fake")
	if err != nil {
		t.Fatalf("Lookup(fake) unexpected error: %v", err)
	}
	if r.Binary != "fakebin" {
		t.Errorf("Binary = %q, want %q", r.Binary, "fakebin")
	}
	if len(r.DefaultArgs) != 1 || r.DefaultArgs[0] != "--yolo" {
		t.Errorf("DefaultArgs = %v, want [--yolo]", r.DefaultArgs)
	}
	if r.Driver == nil || r.Driver.Name() != "fake" {
		t.Errorf("Driver not returned correctly: %+v", r.Driver)
	}
}

// TestLookup_ReturnsFreshInstance verifies each Lookup call produces a new
// driver. This matters because drivers may accumulate per-session state in
// the future (e.g. a startup-time timestamp) and Lookup sharing instances
// would surprise callers.
func TestLookup_ReturnsFreshInstance(t *testing.T) {
	withIsolatedRegistry(t)

	Register(Registration{
		Name:    "fake",
		Binary:  "fakebin",
		Factory: func() Driver { return &fakeDriver{name: "fake"} },
	})

	r1, err := Lookup("fake")
	if err != nil {
		t.Fatal(err)
	}
	r2, err := Lookup("fake")
	if err != nil {
		t.Fatal(err)
	}
	if r1.Driver == r2.Driver {
		t.Error("Lookup returned the same driver instance twice; expected fresh each call")
	}
}

// TestLookup_DefaultArgsAreCopied verifies callers can't mutate the shared
// registry slice by modifying the result they received.
func TestLookup_DefaultArgsAreCopied(t *testing.T) {
	withIsolatedRegistry(t)

	Register(Registration{
		Name:        "fake",
		Binary:      "fakebin",
		DefaultArgs: []string{"--yolo"},
		Factory:     func() Driver { return &fakeDriver{name: "fake"} },
	})

	r, err := Lookup("fake")
	if err != nil {
		t.Fatal(err)
	}
	r.DefaultArgs[0] = "HIJACKED"

	r2, err := Lookup("fake")
	if err != nil {
		t.Fatal(err)
	}
	if r2.DefaultArgs[0] != "--yolo" {
		t.Errorf("mutation of first Lookup leaked into second Lookup: got %q", r2.DefaultArgs[0])
	}
}

func TestLookup_UnknownNameErrors(t *testing.T) {
	withIsolatedRegistry(t)

	Register(Registration{
		Name:    "fake",
		Binary:  "fakebin",
		Factory: func() Driver { return &fakeDriver{name: "fake"} },
	})

	_, err := Lookup("bogus")
	if err == nil {
		t.Fatal("Lookup(bogus) returned nil error, expected failure")
	}
	// Error should include both the bad name and the available options so
	// users get an immediately-actionable message.
	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error should mention the bad name: %v", err)
	}
	if !strings.Contains(err.Error(), "fake") {
		t.Errorf("error should list available drivers: %v", err)
	}
}

func TestRegister_PanicsOnDuplicate(t *testing.T) {
	withIsolatedRegistry(t)

	Register(Registration{
		Name:    "fake",
		Binary:  "fakebin",
		Factory: func() Driver { return &fakeDriver{name: "fake"} },
	})

	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on duplicate registration")
		}
	}()
	Register(Registration{
		Name:    "fake",
		Binary:  "fakebin",
		Factory: func() Driver { return &fakeDriver{name: "fake"} },
	})
}

func TestRegister_PanicsOnMissingFields(t *testing.T) {
	cases := []struct {
		name string
		reg  Registration
	}{
		{"empty name", Registration{Binary: "x", Factory: func() Driver { return &fakeDriver{} }}},
		{"empty binary", Registration{Name: "x", Factory: func() Driver { return &fakeDriver{} }}},
		{"nil factory", Registration{Name: "x", Binary: "x"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			withIsolatedRegistry(t)
			defer func() {
				if r := recover(); r == nil {
					t.Errorf("expected panic for %s", tc.name)
				}
			}()
			Register(tc.reg)
		})
	}
}

func TestRegistered_SortedAndStable(t *testing.T) {
	withIsolatedRegistry(t)

	for _, name := range []string{"charlie", "alpha", "bravo"} {
		name := name
		Register(Registration{
			Name:    name,
			Binary:  "bin-" + name,
			Factory: func() Driver { return &fakeDriver{name: name} },
		})
	}

	got := Registered()
	want := []string{"alpha", "bravo", "charlie"}
	if len(got) != len(want) {
		t.Fatalf("Registered() length = %d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Registered()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
