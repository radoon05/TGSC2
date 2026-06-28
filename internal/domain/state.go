package domain

// SyncState represents the state of a sync job
type SyncState string

const (
	StatePending    SyncState = "PENDING"
	StateRunning    SyncState = "RUNNING"
	StateSuccess    SyncState = "SUCCESS"
	StateFailed     SyncState = "FAILED"
	StateDeadLetter SyncState = "DEAD_LETTER"
)

// String returns the string representation of SyncState
func (s SyncState) String() string {
	return string(s)
}

// IsValid checks if the state is valid
func (s SyncState) IsValid() bool {
	switch s {
	case StatePending, StateRunning, StateSuccess, StateFailed, StateDeadLetter:
		return true
	}
	return false
}

// IsTerminal returns true if the state is a terminal state (no further transitions)
func (s SyncState) IsTerminal() bool {
	return s == StateSuccess || s == StateDeadLetter
}

// CanTransitionTo checks if a transition from current state to next state is allowed
func (s SyncState) CanTransitionTo(next SyncState) bool {
	switch s {
	case StatePending:
		return next == StateRunning
	case StateRunning:
		return next == StateSuccess || next == StateFailed
	case StateFailed:
		return next == StatePending || next == StateDeadLetter
	case StateSuccess, StateDeadLetter:
		return false // terminal states
	default:
		return false
	}
}