// SPDX-License-Identifier: Apache-2.0

package supervisor

import (
	"slices"
	"time"
)

// State is the lifecycle state of a supervised component.
type State int

// Component states, as reported by [Supervisor.Status].
const (
	StatePending  State = iota // registered, not started (not yet running, or its roles are not selected)
	StateStarting              // goroutine launched
	StateRunning               // inside Run
	StateBackoff               // failed; waiting to restart
	StateDone                  // Run returned nil before shutdown
	StateFailed                // failed and will not be restarted
	StateStopped               // stopped by shutdown
)

func (s State) String() string {
	switch s {
	case StatePending:
		return "pending"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateBackoff:
		return "backoff"
	case StateDone:
		return "done"
	case StateFailed:
		return "failed"
	case StateStopped:
		return "stopped"
	}
	return "unknown"
}

// ComponentStatus is a point-in-time snapshot of one component.
type ComponentStatus struct {
	Name      string
	Roles     []string
	Stage     Stage
	Restart   Restart
	State     State
	Restarts  int       // restarts performed so far
	LastError error     // most recent failure, if any
	Since     time.Time // when State was entered
}

// Status returns a snapshot of every registered component, in registration
// order. It is intended for health endpoints, logs and CLI output.
func (s *Supervisor) Status() []ComponentStatus {
	s.mu.Lock()
	entries := slices.Clone(s.entries)
	s.mu.Unlock()

	out := make([]ComponentStatus, 0, len(entries))
	for _, e := range entries {
		e.mu.Lock()
		out = append(out, ComponentStatus{
			Name:      e.spec.Component.Name(),
			Roles:     slices.Clone(e.spec.Roles),
			Stage:     e.spec.Stage,
			Restart:   e.spec.Restart,
			State:     e.state,
			Restarts:  e.restarts,
			LastError: e.lastErr,
			Since:     e.since,
		})
		e.mu.Unlock()
	}
	return out
}
