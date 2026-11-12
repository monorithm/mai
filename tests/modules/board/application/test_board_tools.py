from typing import cast

import anyio
import pytest

from mai.modules.board import MIGRATIONS, BoardTools
from mai.modules.board.domain.board_store import BoardStore
from mai.modules.board.infra.memory_board_store import MemoryBoardStore
from mai.modules.board.infra.postgres_board_store import TOOLS
from mai.modules.sessions import SessionService
from mai.platform.contract import Ctx, ToolError

NAMES = {
    "get_board",
    "read_item",
    "start_item",
    "report_status",
    "submit_for_review",
    "plan_item",
    "assign_item",
    "review_item",
    "manage_sprint",
}


class FakeState:
    handle = "ses-abc"


class FakeSessions:
    def __init__(self):
        self.seen = []

    async def resolve(self, handle):
        self.seen.append(handle)
        return FakeState()


class RecordingStore(BoardStore):
    def __init__(self):
        self.calls = []

    async def call(self, tool, ctx, args, state):
        self.calls.append((tool, args, state))
        return {"ok": tool}


def sessions_of(fake: FakeSessions) -> SessionService:
    # The fake gives the resolve call of the real service; the cast states that.
    return cast(SessionService, fake)


def run(coro):
    return anyio.run(lambda: coro)


def test_tool_names_match_the_store():
    tools = BoardTools(RecordingStore(), sessions_of(FakeSessions())).tools()
    assert set(tools) == NAMES == set(TOOLS)


def test_session_argument_resolves_first():
    store, sessions = RecordingStore(), FakeSessions()
    tools = BoardTools(store, sessions_of(sessions)).tools()
    out = anyio.run(tools["get_board"], Ctx(None, "http"), {"session": "ses-x"})
    assert out == {"ok": "get_board"} and sessions.seen == ["ses-x"]
    assert isinstance(store.calls[0][2], FakeState)


def test_not_logged_in_and_unknown_session():
    tools = BoardTools(RecordingStore(), sessions_of(FakeSessions())).tools()
    with pytest.raises(ToolError) as e:
        anyio.run(tools["plan_item"], Ctx(None, "stdio"), {})
    assert e.value.code == "NOT_LOGGED_IN"
    with pytest.raises(ToolError) as e:
        anyio.run(tools["plan_item"], Ctx(None, "http"), {})
    assert e.value.code == "SESSION_UNKNOWN"
    with pytest.raises(ToolError) as e:
        anyio.run(tools["plan_item"], Ctx(None, "http"), ["x"])
    assert e.value.code == "INVALID_ARGUMENT"


def test_memory_store_is_unavailable():
    with pytest.raises(ToolError) as e:
        anyio.run(MemoryBoardStore().call, "get_board", None, {}, FakeState())
    assert e.value.code == "STORE_UNAVAILABLE"


def test_migrations_range():
    assert [m.version for m in MIGRATIONS] == [500]
    assert all(500 <= m.version < 600 for m in MIGRATIONS)
    assert "CREATE TABLE board.item" in MIGRATIONS[0].sql


class FakeCursor:
    def __init__(self, project):
        self.project = project

    def execute(self, sql, params=()):
        self.sql = sql

    def fetchone(self):
        return self.project if "FROM board.project" in self.sql else None


class FakeConn:
    def __init__(self, project):
        self.project, self.rolled_back = project, False

    def cursor(self, row_factory=None):
        return FakeCursor(self.project)

    def rollback(self):
        self.rolled_back = True

    def close(self):
        pass


class RoleState:
    login, agent, kind, handle = "cursor", "cursor", "session", "ses-abc"

    def __init__(self):
        self.roles = {"api": "worker"}


def test_rule_error_becomes_tool_error():
    try:
        import psycopg  # noqa: F401
    except ImportError:
        pytest.skip("psycopg needs libpq")
    from mai.modules.board.infra.postgres_board_store import PostgresBoardStore

    conn = FakeConn({"name": "api", "wip_limit": 1})
    store = PostgresBoardStore(lambda: conn, mailer=None, claims=None)
    with pytest.raises(ToolError) as e:
        anyio.run(
            store.call,
            "plan_item",
            None,
            {"project": "api", "request_id": None},
            RoleState(),
        )
    assert e.value.code == "FORBIDDEN_ROLE" and conn.rolled_back
