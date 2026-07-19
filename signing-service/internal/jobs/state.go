package jobs

import "fmt"

type State string

const (
	StateUploaded                  State = "UPLOADED"
	StateValidated                 State = "VALIDATED"
	StateAwaitingOwnerConfirmation State = "AWAITING_OWNER_CONFIRMATION"
	StateQueued                    State = "QUEUED"
	StateWaitingForToken           State = "WAITING_FOR_TOKEN"
	StatePINRequired               State = "PIN_REQUIRED"
	StateAuthorizationConsumed     State = "AUTHORIZATION_CONSUMED"
	StateSigning                   State = "SIGNING"
	StateTimestamping              State = "TIMESTAMPING"
	StateVerifying                 State = "VERIFYING"
	StateCompleted                 State = "COMPLETED"

	StateRejectedInput       State = "REJECTED_INPUT"
	StateDeclined            State = "DECLINED"
	StateCancelled           State = "CANCELLED"
	StateConfirmationExpired State = "CONFIRMATION_EXPIRED"
	StateApprovalExpired     State = "APPROVAL_EXPIRED"
	StatePINWindowExpired    State = "PIN_WINDOW_EXPIRED"
	StatePINRejected         State = "PIN_REJECTED"
	StateTokenLocked         State = "TOKEN_LOCKED"
	StateTokenUnavailable    State = "TOKEN_UNAVAILABLE"
	StateFailedPreSign       State = "FAILED_PRE_SIGN"
	StateFailedPostSign      State = "FAILED_POST_SIGN"
	StateOutcomeUnknown      State = "OUTCOME_UNKNOWN"
	StateQuarantined         State = "QUARANTINED"
)

// transitions encodes the legal signer state machine (plan §7.2, §7.3):
// SIGNING is reachable only via AUTHORIZATION_CONSUMED, which is reachable only
// from PIN_REQUIRED after durable challenge consumption; no CANCELLED edge
// exists once authorization is consumed.
var transitions = map[State]map[State]struct{}{
	StateUploaded: {
		StateValidated: {}, StateRejectedInput: {}, StateCancelled: {},
	},
	StateValidated: {
		StateAwaitingOwnerConfirmation: {}, StateRejectedInput: {}, StateCancelled: {},
	},
	StateAwaitingOwnerConfirmation: {
		StateQueued: {}, StateDeclined: {}, StateConfirmationExpired: {}, StateCancelled: {},
	},
	StateQueued: {
		StatePINRequired: {}, StateWaitingForToken: {}, StateTokenUnavailable: {},
		StateFailedPreSign: {}, StateApprovalExpired: {}, StateCancelled: {},
	},
	StateWaitingForToken: {
		StateQueued: {}, StatePINRequired: {}, StateTokenUnavailable: {},
		StateFailedPreSign: {}, StateApprovalExpired: {}, StateCancelled: {},
	},
	StatePINRequired: {
		StateAuthorizationConsumed: {}, StatePINWindowExpired: {}, StateTokenUnavailable: {},
		StateFailedPreSign: {}, StateApprovalExpired: {}, StateCancelled: {},
	},
	StateAuthorizationConsumed: {
		StateSigning: {}, StatePINRejected: {}, StateTokenLocked: {},
		StateTokenUnavailable: {}, StateFailedPreSign: {},
	},
	StateSigning: {
		StateVerifying: {}, StateTimestamping: {}, StateFailedPostSign: {},
		StateOutcomeUnknown: {}, StateQuarantined: {},
	},
	StateTimestamping: {
		StateVerifying: {}, StateFailedPostSign: {}, StateQuarantined: {},
	},
	StateVerifying: {
		StateCompleted: {}, StateFailedPostSign: {}, StateQuarantined: {},
	},
}

var terminalStates = map[State]struct{}{
	StateCompleted:           {},
	StateRejectedInput:       {},
	StateDeclined:            {},
	StateCancelled:           {},
	StateConfirmationExpired: {},
	StateApprovalExpired:     {},
	StatePINWindowExpired:    {},
	StatePINRejected:         {},
	StateTokenLocked:         {},
	StateTokenUnavailable:    {},
	StateFailedPreSign:       {},
	StateFailedPostSign:      {},
	StateOutcomeUnknown:      {},
	StateQuarantined:         {},
}

// IsKnown reports whether s is a recognized signer state.
func (s State) IsKnown() bool {
	if _, ok := terminalStates[s]; ok {
		return true
	}
	_, ok := transitions[s]
	return ok
}

func (s State) IsTerminal() bool {
	_, ok := terminalStates[s]
	return ok
}

// CancellationAllowed reports whether a user cancellation may still be honored
// from state s (plan §7.3: refused once authorization is consumed).
func CancellationAllowed(s State) bool {
	if s.IsTerminal() {
		return false
	}
	allowed, known := transitions[s]
	if !known {
		return false
	}
	_, ok := allowed[StateCancelled]
	return ok
}

func ValidateTransition(from, to State, timestampingEnabled bool) error {
	if from.IsTerminal() {
		return fmt.Errorf("terminal signing state %q is immutable", from)
	}
	if to == StateTimestamping && !timestampingEnabled {
		return fmt.Errorf("timestamping is disabled by policy")
	}
	allowed, known := transitions[from]
	if !known {
		return fmt.Errorf("unknown or non-transitioning signing state %q", from)
	}
	if _, ok := allowed[to]; !ok {
		return fmt.Errorf("illegal signing state transition %q -> %q", from, to)
	}
	return nil
}
