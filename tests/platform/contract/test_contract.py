import pytest

from mai.platform.contract import MESSAGES, RETRY_SAFE, Ctx, ToolError

DESIGN_CODES = {
    "INVALID_ARGUMENT",
    "LOGIN_FAILED",
    "CREDENTIAL_EXPIRED",
    "CREDENTIAL_REVOKED",
    "SESSION_UNKNOWN",
    "NOT_LOGGED_IN",
    "FORBIDDEN_ROLE",
    "RECIPIENT_UNKNOWN",
    "UNKNOWN_MESSAGE",
    "BODY_TOO_LARGE",
    "REFUSED_HEADER",
    "RETRY_KEY_REUSED",
    "THREAD_CLAIMED",
    "MAIL_SERVER_DOWN",
    "STORE_UNAVAILABLE",
    "BUSY",
    "INTERNAL",
    "BRIDGE_ONLY",
}
BOARD_CODES = {
    "INVALID_TRANSITION",
    "WIP_LIMIT",
    "SUPERVISOR_TAKEN",
    "EVIDENCE_INCOMPLETE",
    "DEPENDENCY_OPEN",
    "UNKNOWN_ITEM",
    "UNKNOWN_PROJECT",
}

LEASE_CODES = {"NO_FREE_SESSION"}


def test_each_design_board_and_lease_code_has_a_message() -> None:
    assert set(MESSAGES) == DESIGN_CODES | BOARD_CODES | LEASE_CODES


def test_no_free_session_tells_the_chat_to_give_a_named_session_or_to_try_again() -> (
    None
):
    assert ToolError("NO_FREE_SESSION").structured() == {
        "error": {
            "code": "NO_FREE_SESSION",
            "message": (
                "No pool session of the agent is free. "
                "Give the label of a named session as session, or try again later."
            ),
            "path": None,
            "retry_safe": True,
        }
    }


def test_only_the_transient_codes_are_retry_safe() -> None:
    assert RETRY_SAFE == {
        "THREAD_CLAIMED",
        "NO_FREE_SESSION",
        "MAIL_SERVER_DOWN",
        "STORE_UNAVAILABLE",
        "BUSY",
        "INTERNAL",
    }


def test_the_structured_error_has_the_contract_fields_and_extras() -> None:
    err = ToolError("THREAD_CLAIMED", path="/ref", lease_until="2026-11-12T09:30:00Z")
    assert err.structured() == {
        "error": {
            "code": "THREAD_CLAIMED",
            "message": MESSAGES["THREAD_CLAIMED"],
            "path": "/ref",
            "retry_safe": True,
        },
        "lease_until": "2026-11-12T09:30:00Z",
    }


def test_an_unknown_code_becomes_internal() -> None:
    assert ToolError("NO_SUCH_CODE").code == "INTERNAL"


def test_the_context_checks_the_transport() -> None:
    assert not Ctx().logged_in
    assert Ctx(session_state=object(), transport="http").logged_in
    with pytest.raises(ValueError):
        Ctx(transport="tcp")
