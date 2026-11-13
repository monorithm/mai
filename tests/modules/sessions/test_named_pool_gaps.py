"""A named session of the owner and the pool sessions of the lease API stay apart."""

import anyio
import pytest

from mai.modules.sessions.application.lease_service import LeaseError
from mai.modules.sessions.application.owner_sessions import OwnerSessions
from tests.modules.sessions.test_lease_service import make_parts


def test_the_owner_cannot_make_a_named_session_with_a_pool_label(tmp_path):
    parts = make_parts(tmp_path)
    owner = OwnerSessions(parts.credentials, parts.leases)
    # "mai session new cursor --label s1" asks for the named session cursor.s1.
    for label in ("s1", "s9", "s01"):
        with pytest.raises(LeaseError) as info:
            anyio.run(owner.new, "cursor", label)
        assert info.value.code == "BAD_LABEL", label
        assert "--count" in str(info.value)
    assert anyio.run(parts.credentials.session_list, "cursor") == []
    # A label that only starts like a pool label is a named session.
    for label in ("s1x", "sa", "session"):
        assert anyio.run(owner.new, "cursor", label)["login"] == f"cursor.{label}"

    # A bridge starts and asks for a pool session (no session name).
    pool = anyio.run(parts.leases.acquire, parts.key, "claude", "/work/other", 1)
    assert pool["session"] == "cursor.s1"


def test_a_lease_request_for_a_pool_label_gives_bad_label(tmp_path):
    parts = make_parts(tmp_path)
    owner = OwnerSessions(parts.credentials, parts.leases)
    anyio.run(owner.new_pool, "cursor", 2)
    for name in ("s2", "cursor.s2", "s3"):
        with pytest.raises(LeaseError) as info:
            anyio.run(parts.leases.acquire, parts.key, "cursor", "/w", 2, name)
        assert info.value.code == "BAD_LABEL", name
        assert "pool" in str(info.value) and "login" in str(info.value)
    assert anyio.run(parts.leases.leases) == []
