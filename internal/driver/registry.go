package driver

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// Factory constructs a fresh Driver. Registered by each driver sub-package
// in its init() function so main doesn't need to import every driver package
// explicitly (it imports the driver package once and pulls in sub-packages
// via side-effect imports).
type Factory func() Driver

var (
	registryMu sync.RWMutex
	registry   = map[string]registryEntry{}
)

// registryEntry pairs the per-agent Factory with the binary info needed to
// spawn the agent process. Keeping both in one place means the CLI has a
// single source of truth per agent.
type registryEntry struct {
	// Binary is the executable name (PATH-resolved) to spawn.
	Binary string
	// DefaultArgs are args always prepended to the user-supplied args.
	// Drivers use this to encode mandatory flags (e.g. codex's --yolo).
	DefaultArgs []string
	// Factory produces a fresh driver instance.
	Factory Factory
}

// Registration describes an agent to register.
type Registration struct {
	Name        string
	Binary      string
	DefaultArgs []string
	Factory     Factory
}

// Register adds a driver to the global registry. It must be called from an
// init() function in the driver sub-package. Panics on duplicate registration
// or missing fields — those are programmer errors, not runtime errors.
func Register(r Registration) {
	if r.Name == "" {
		panic("driver.Register: empty name")
	}
	if r.Binary == "" {
		panic(fmt.Sprintf("driver.Register(%q): empty binary", r.Name))
	}
	if r.Factory == nil {
		panic(fmt.Sprintf("driver.Register(%q): nil Factory", r.Name))
	}

	registryMu.Lock()
	defer registryMu.Unlock()
	if _, exists := registry[r.Name]; exists {
		panic(fmt.Sprintf("driver.Register(%q): already registered", r.Name))
	}
	registry[r.Name] = registryEntry{
		Binary:      r.Binary,
		DefaultArgs: append([]string(nil), r.DefaultArgs...),
		Factory:     r.Factory,
	}
}

// Resolved is what Lookup returns: a fresh driver instance plus the spawn
// info the CLI needs to actually run the agent.
type Resolved struct {
	Driver      Driver
	Binary      string
	DefaultArgs []string
}

// Lookup returns the driver and spawn info for the named agent, or an error
// listing the available agents if the name is unknown.
func Lookup(name string) (Resolved, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()

	entry, ok := registry[name]
	if !ok {
		return Resolved{}, fmt.Errorf("unknown agent %q (available: %s)", name, strings.Join(registered(), ", "))
	}
	return Resolved{
		Driver:      entry.Factory(),
		Binary:      entry.Binary,
		DefaultArgs: append([]string(nil), entry.DefaultArgs...),
	}, nil
}

// Registered returns the names of all registered drivers in sorted order.
// Useful for CLI help text and error messages.
func Registered() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	return registered()
}

// registered assumes the caller holds registryMu (read or write).
func registered() []string {
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
