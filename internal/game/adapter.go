// Package game defines the adapter contract that turns a stored Task into an
// external-process invocation, plus a registry of the available adapters.
//
// Adapters NEVER inject code, read/write game memory, manipulate packets, or
// implement anti-detection logic. They only translate configuration into a
// command line for an already-installed local tool, which runner then spawns
// as a child process.
package game

import (
	"fmt"
	"sort"

	"github.com/xiabee/game-scheduler/internal/runner"
	"github.com/xiabee/game-scheduler/internal/store"
)

// Adapter builds a runnable command for one game's automation tool.
type Adapter interface {
	// Key is the stable identifier stored in Game.Adapter.
	Key() string
	// Validate checks that the game configuration is usable by this adapter.
	Validate(g store.Game) error
	// BuildCommand translates a task (in the context of its game) into a spec
	// that runner can execute.
	BuildCommand(g store.Game, t store.Task) (runner.Spec, error)
	// TaskTypes returns the task types this adapter understands, for docs/UX.
	TaskTypes() []string
}

// Registry maps adapter keys to implementations.
type Registry struct {
	m map[string]Adapter
}

// NewRegistry builds a registry from the given adapters.
func NewRegistry(adapters ...Adapter) *Registry {
	r := &Registry{m: map[string]Adapter{}}
	for _, a := range adapters {
		r.m[a.Key()] = a
	}
	return r
}

// Get returns the adapter for key.
func (r *Registry) Get(key string) (Adapter, error) {
	a, ok := r.m[key]
	if !ok {
		return nil, fmt.Errorf("game: no adapter registered for %q", key)
	}
	return a, nil
}

// Keys returns the registered adapter keys, sorted.
func (r *Registry) Keys() []string {
	keys := make([]string, 0, len(r.m))
	for k := range r.m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
