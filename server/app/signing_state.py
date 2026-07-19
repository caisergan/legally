"""FastAPI-side projection of the canonical signing state model (§7).

FastAPI owns ownership/upload/confirmation/download states and projects the
durable signer states without inventing transitions. This module encodes the
state vocabulary, the FastAPI-authoritative transitions, terminality, and
cancellation eligibility so callers never hand-roll these rules.
"""

from __future__ import annotations

# Pre-signer, FastAPI-authoritative lifecycle.
UPLOADED = "UPLOADED"
VALIDATED = "VALIDATED"
AWAITING_OWNER_CONFIRMATION = "AWAITING_OWNER_CONFIRMATION"

# Signer-authoritative lifecycle (projected from durable signer events).
QUEUED = "QUEUED"
WAITING_FOR_TOKEN = "WAITING_FOR_TOKEN"
PIN_REQUIRED = "PIN_REQUIRED"
AUTHORIZATION_CONSUMED = "AUTHORIZATION_CONSUMED"
SIGNING = "SIGNING"
TIMESTAMPING = "TIMESTAMPING"  # disabled in the initial slice
VERIFYING = "VERIFYING"
COMPLETED = "COMPLETED"

# Terminal states.
REJECTED_INPUT = "REJECTED_INPUT"
DECLINED = "DECLINED"
CANCELLED = "CANCELLED"
CONFIRMATION_EXPIRED = "CONFIRMATION_EXPIRED"
APPROVAL_EXPIRED = "APPROVAL_EXPIRED"
PIN_WINDOW_EXPIRED = "PIN_WINDOW_EXPIRED"
PIN_REJECTED = "PIN_REJECTED"
TOKEN_LOCKED = "TOKEN_LOCKED"
TOKEN_UNAVAILABLE = "TOKEN_UNAVAILABLE"
FAILED_PRE_SIGN = "FAILED_PRE_SIGN"
FAILED_POST_SIGN = "FAILED_POST_SIGN"
OUTCOME_UNKNOWN = "OUTCOME_UNKNOWN"
QUARANTINED = "QUARANTINED"

ALL_STATES: frozenset[str] = frozenset(
    {
        UPLOADED,
        VALIDATED,
        AWAITING_OWNER_CONFIRMATION,
        QUEUED,
        WAITING_FOR_TOKEN,
        PIN_REQUIRED,
        AUTHORIZATION_CONSUMED,
        SIGNING,
        TIMESTAMPING,
        VERIFYING,
        COMPLETED,
        REJECTED_INPUT,
        DECLINED,
        CANCELLED,
        CONFIRMATION_EXPIRED,
        APPROVAL_EXPIRED,
        PIN_WINDOW_EXPIRED,
        PIN_REJECTED,
        TOKEN_LOCKED,
        TOKEN_UNAVAILABLE,
        FAILED_PRE_SIGN,
        FAILED_POST_SIGN,
        OUTCOME_UNKNOWN,
        QUARANTINED,
    }
)

TERMINAL_STATES: frozenset[str] = frozenset(
    {
        COMPLETED,
        REJECTED_INPUT,
        DECLINED,
        CANCELLED,
        CONFIRMATION_EXPIRED,
        APPROVAL_EXPIRED,
        PIN_WINDOW_EXPIRED,
        PIN_REJECTED,
        TOKEN_LOCKED,
        TOKEN_UNAVAILABLE,
        FAILED_PRE_SIGN,
        FAILED_POST_SIGN,
        OUTCOME_UNKNOWN,
        QUARANTINED,
    }
)

# States FastAPI may set directly on the local request before/around signer
# dispatch. Signer-owned states arrive only through projected events.
_FASTAPI_TRANSITIONS: dict[str, frozenset[str]] = {
    UPLOADED: frozenset({VALIDATED, REJECTED_INPUT}),
    VALIDATED: frozenset({AWAITING_OWNER_CONFIRMATION, REJECTED_INPUT}),
    AWAITING_OWNER_CONFIRMATION: frozenset(
        {QUEUED, DECLINED, CONFIRMATION_EXPIRED, CANCELLED}
    ),
}

# Once authorization is consumed, cancellation is refused even before C_Sign.
_CANCELLABLE: frozenset[str] = frozenset(
    {
        UPLOADED,
        VALIDATED,
        AWAITING_OWNER_CONFIRMATION,
        QUEUED,
        WAITING_FOR_TOKEN,
        PIN_REQUIRED,
    }
)


def is_known(state: str) -> bool:
    return state in ALL_STATES


def is_terminal(state: str) -> bool:
    return state in TERMINAL_STATES


def cancellation_allowed(state: str) -> bool:
    return state in _CANCELLABLE


def fastapi_can_transition(current: str, target: str) -> bool:
    """True only for transitions FastAPI itself is authoritative for (§7.2)."""
    return target in _FASTAPI_TRANSITIONS.get(current, frozenset())
