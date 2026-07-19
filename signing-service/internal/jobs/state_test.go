package jobs

import "testing"

func TestHappyPathTransitions(t *testing.T) {
	states := []State{
		StateUploaded,
		StateValidated,
		StateAwaitingOwnerConfirmation,
		StateQueued,
		StatePINRequired,
		StateAuthorizationConsumed,
		StateSigning,
		StateVerifying,
		StateCompleted,
	}
	for index := 0; index < len(states)-1; index++ {
		if err := ValidateTransition(states[index], states[index+1], false); err != nil {
			t.Fatalf("transition %s -> %s rejected: %v", states[index], states[index+1], err)
		}
	}
}

func TestSigningIsReachableOnlyThroughAuthorizationConsumed(t *testing.T) {
	if err := ValidateTransition(StatePINRequired, StateSigning, false); err == nil {
		t.Fatal("PIN_REQUIRED transitioned directly to SIGNING, bypassing AUTHORIZATION_CONSUMED")
	}
	if err := ValidateTransition(StatePINRequired, StateAuthorizationConsumed, false); err != nil {
		t.Fatalf("PIN_REQUIRED -> AUTHORIZATION_CONSUMED rejected: %v", err)
	}
	if err := ValidateTransition(StateAuthorizationConsumed, StateSigning, false); err != nil {
		t.Fatalf("AUTHORIZATION_CONSUMED -> SIGNING rejected: %v", err)
	}
}

func TestCancellationStopsWhenAuthorizationIsConsumed(t *testing.T) {
	if !CancellationAllowed(StatePINRequired) {
		t.Fatal("pre-authorization cancellation was refused")
	}
	if err := ValidateTransition(StatePINRequired, StateCancelled, false); err != nil {
		t.Fatalf("pre-authorization cancellation rejected: %v", err)
	}
	// §7.3: once authorization is consumed, cancellation is refused even before C_Sign.
	if CancellationAllowed(StateAuthorizationConsumed) {
		t.Fatal("cancellation was allowed after authorization was consumed")
	}
	if err := ValidateTransition(StateAuthorizationConsumed, StateCancelled, false); err == nil {
		t.Fatal("cancellation was accepted after authorization was consumed")
	}
	if CancellationAllowed(StateSigning) {
		t.Fatal("cancellation was allowed after signing began")
	}
	if err := ValidateTransition(StateSigning, StateCancelled, false); err == nil {
		t.Fatal("cancellation was accepted after signing began")
	}
}

func TestAuthorizationConsumedFailuresImplyNoSignature(t *testing.T) {
	// A single login attempt may fail from AUTHORIZATION_CONSUMED without a
	// signature having executed; none of these edges imply post-sign state.
	for _, to := range []State{StatePINRejected, StateTokenLocked, StateTokenUnavailable, StateFailedPreSign} {
		if err := ValidateTransition(StateAuthorizationConsumed, to, false); err != nil {
			t.Fatalf("AUTHORIZATION_CONSUMED -> %s rejected: %v", to, err)
		}
	}
	if err := ValidateTransition(StateAuthorizationConsumed, StateFailedPostSign, false); err == nil {
		t.Fatal("AUTHORIZATION_CONSUMED reached a post-sign failure without SIGNING")
	}
}

func TestApprovalCanExpireFromQueuedWaitingAndPINRequired(t *testing.T) {
	for _, from := range []State{StateQueued, StateWaitingForToken, StatePINRequired} {
		if err := ValidateTransition(from, StateApprovalExpired, false); err != nil {
			t.Fatalf("%s -> APPROVAL_EXPIRED rejected: %v", from, err)
		}
	}
	if !StateApprovalExpired.IsTerminal() {
		t.Fatal("APPROVAL_EXPIRED must be terminal")
	}
	// Approvals are never silently refreshed back to an active state.
	if err := ValidateTransition(StateApprovalExpired, StateQueued, false); err == nil {
		t.Fatal("APPROVAL_EXPIRED was refreshed back into the queue")
	}
}

func TestTerminalStatesAreImmutable(t *testing.T) {
	for state := range terminalStates {
		t.Run(string(state), func(t *testing.T) {
			if err := ValidateTransition(state, StateQueued, false); err == nil {
				t.Fatalf("terminal state %s accepted a transition", state)
			}
		})
	}
}

func TestTimestampingRequiresPolicyEnablement(t *testing.T) {
	if err := ValidateTransition(StateSigning, StateTimestamping, false); err == nil {
		t.Fatal("timestamping was accepted while disabled")
	}
	if err := ValidateTransition(StateSigning, StateTimestamping, true); err != nil {
		t.Fatalf("enabled timestamping transition rejected: %v", err)
	}
}

func TestOutcomeUnknownCannotBeRequeued(t *testing.T) {
	if err := ValidateTransition(StateOutcomeUnknown, StateQueued, false); err == nil {
		t.Fatal("OUTCOME_UNKNOWN was requeued")
	}
}
