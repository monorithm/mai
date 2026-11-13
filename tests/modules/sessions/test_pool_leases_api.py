"""The pool labels and the lease with no session in the owner API and the lease API."""

from mai.modules.sessions.domain.lease import MAX_SESSIONS
from tests.modules.sessions.test_sessions_api import AUTH, api, lease, owner_answer

__all__ = ["api"]
# The contract limit of the leases with no session for each agent.
UNBOUND_LIMIT = 16


def heartbeat(client, token):
    return client.post(
        "/api/leases/current/heartbeat", headers={"authorization": f"Bearer {token}"}
    )


def test_the_owner_api_refuses_a_pool_label_and_keeps_the_count(api):
    client, _key = api
    for label in ("s1", "s9", "s01"):
        made = client.post(
            "/api/sessions", json={"agent": "cursor", "label": label}, headers=AUTH
        )
        assert made.status_code == 400, label
        error = owner_answer(made)["error"]
        assert error["code"] == "BAD_LABEL" and "--count" in error["message"]
    pool = client.post(
        "/api/sessions", json={"agent": "cursor", "count": 2}, headers=AUTH
    )
    assert pool.status_code == 201
    assert [s["login"] for s in owner_answer(pool)["sessions"]] == [
        "cursor.s1",
        "cursor.s2",
    ]


def test_a_lease_request_for_a_pool_label_gives_400_bad_label(api):
    client, key = api
    client.post("/api/sessions", json={"agent": "cursor", "count": 2}, headers=AUTH)
    for name in ("s2", "cursor.s2", "s7"):
        answer = lease(client, key, session=name)
        assert answer.status_code == 400, name
        error = answer.json()["error"]
        assert error["code"] == "BAD_LABEL"
        assert "pool" in error["message"] and "login" in error["message"]
    # A session of a different agent stays unknown.
    assert lease(client, key, session="codex.s2").json()["error"]["code"] == (
        "SESSION_UNKNOWN"
    )


def test_a_full_pool_gives_a_lease_with_session_null(api):
    client, key = api
    pool = [lease(client, key, pid=pid) for pid in range(MAX_SESSIONS)]
    assert all(answer.status_code == 201 for answer in pool)
    extra = lease(client, key, pid=100)
    assert extra.status_code == 201
    body = extra.json()
    assert body["session"] is None
    assert set(body) == {"lease", "session", "heartbeat_seconds", "expires_seconds"}
    beat = heartbeat(client, body["lease"])
    assert beat.status_code == 200
    assert beat.json() == {"session": None, "new_mail": 0}

    leases = client.get("/api/leases", headers=AUTH).json()["leases"]
    assert [row["session"] for row in leases][-1] is None
    assert leases[-1]["pid"] == 100
    sessions = owner_answer(client.get("/api/sessions", headers=AUTH))["sessions"]
    assert len(sessions) == MAX_SESSIONS
    assert all(row["login"] and row["leased"] for row in sessions)
    agents = client.get("/api/agents", headers=AUTH).json()["agents"]
    assert agents == [{"name": "cursor", "sessions": MAX_SESSIONS, "leases": 9}]

    for pid in range(1, UNBOUND_LIMIT):
        assert lease(client, key, pid=100 + pid).json()["session"] is None
    full = lease(client, key, pid=200)
    assert full.status_code == 409
    assert full.json()["error"]["code"] == "NO_FREE_SESSION"

    # The owner end of the lease of cursor.s1 does not end a lease with no session.
    ended = client.delete("/api/sessions/cursor.s1/lease", headers=AUTH)
    assert ended.status_code == 204
    assert heartbeat(client, body["lease"]).json() == {"session": None, "new_mail": 0}
    again = lease(client, key, pid=300)
    assert again.status_code == 201 and again.json()["session"] == "cursor.s1"
