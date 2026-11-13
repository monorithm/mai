"""A name with a line break at the end is not a valid label, agent name, login, or lease token."""

import anyio
import pytest

from mai.modules.credentials.domain.credential import (
    check_agent_name,
    check_label,
    parse_credential,
)
from mai.modules.credentials.domain.errors import CredentialError
from mai.modules.sessions.application.lease_service import LeaseError
from mai.modules.sessions.application.owner_sessions import OwnerSessions
from mai.modules.sessions.domain.lease import is_token, new_token
from tests.modules.sessions.test_lease_service import make_parts
from tests.modules.sessions.test_sessions_api import AUTH, api, owner_answer

__all__ = ["api"]


def test_the_owner_api_refuses_a_pool_label_with_a_line_break_at_the_end(api):
    client, _key = api
    # JSON can hold a line break. "s1\n" is the pool label s1 with a line break.
    made = client.post(
        "/api/sessions", json={"agent": "cursor", "label": "s1\n"}, headers=AUTH
    )
    assert made.status_code == 400, owner_answer(made)
    assert owner_answer(made)["error"]["code"] == "BAD_LABEL"
    listed = owner_answer(client.get("/api/sessions", headers=AUTH))["sessions"]
    assert listed == []


def test_owner_sessions_new_refuses_a_pool_label_with_a_line_break_at_the_end(
    tmp_path,
):
    parts = make_parts(tmp_path)
    owner = OwnerSessions(parts.credentials, parts.leases)
    for label in ("s1\n", "lane-a\n"):
        with pytest.raises(LeaseError) as info:
            anyio.run(owner.new, "cursor", label)
        assert info.value.code == "BAD_LABEL", repr(label)
    assert anyio.run(parts.credentials.session_list, "cursor") == []


def test_a_lease_request_for_a_pool_label_with_a_line_break_gives_bad_label(
    tmp_path,
):
    parts = make_parts(tmp_path)
    anyio.run(parts.credentials.session_new, "cursor", "lane-a")
    for name in ("s2\n", "cursor.s2\n", "lane-a\n", "cursor.lane-a\n"):
        with pytest.raises(LeaseError) as info:
            anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 1, name)
        assert info.value.code == "BAD_LABEL", repr(name)
    assert anyio.run(parts.leases.leases) == []


def test_the_owner_api_refuses_an_agent_name_with_a_line_break_at_the_end(api):
    client, _key = api
    made = client.post("/api/agents", json={"name": "codex\n"}, headers=AUTH)
    # A 201 answer holds the agent key. The check does not show the answer.
    status = made.status_code
    assert status == 400
    assert made.json()["error"]["code"] == "BAD_REQUEST"


def test_each_name_check_refuses_a_line_break_at_the_end():
    with pytest.raises(ValueError):
        check_label("s1\n")
    with pytest.raises(ValueError):
        check_agent_name("cursor\n")
    with pytest.raises(CredentialError):
        parse_credential("mai1:cursor.s1\n:" + "a" * 52)
    token = new_token()
    assert is_token(token)
    assert not is_token(token + "\n")
    # The names with no line break stay valid.
    assert check_label("lane-a") == "lane-a"
    assert check_agent_name("cursor") == "cursor"
    assert parse_credential("mai1:cursor.s1:" + "a" * 52)[0] == "cursor.s1"
