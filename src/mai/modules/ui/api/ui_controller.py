"""The owner UI under /ui.

The owner signs in with the owner credential. The sign-in sets an HttpOnly,
SameSite=Strict cookie. Each POST must carry the CSRF token of the sign-in.
All pages are made on the server, with no script, under a strict CSP.
"""

from __future__ import annotations

from urllib.parse import parse_qs

from bustan import Controller, Get, HttpRequest, HttpResponse, Post

from ..application.board_snapshot_query import BoardSnapshotHandler, BoardSnapshotQuery
from ..application.current_session_query import (
    CurrentSessionHandler,
    CurrentSessionQuery,
)
from ..application.manage_sessions_command import (
    ManageSessionsCommand,
    ManageSessionsHandler,
)
from ..application.sign_in_command import SignInCommand, SignInHandler
from ..application.sign_out_command import SignOutCommand, SignOutHandler
from ..domain.owner_session import COOKIE, OwnerSession, csrf_matches, session_cookie
from . import ui_pages as pages

MAX_FORM_BYTES = 16 * 1024


def _error_text(exc: Exception) -> str:
    code = getattr(exc, "code", None)
    return str(code) if code else type(exc).__name__


async def _form(request: HttpRequest) -> dict[str, str]:
    if not request.headers.get("content-type", "").startswith(
        "application/x-www-form-urlencoded"
    ):
        return {}
    body = await request.body()
    if len(body) > MAX_FORM_BYTES:
        return {}
    data = parse_qs(body.decode("utf-8", "replace"), keep_blank_values=True)
    return {k: v[0] for k, v in data.items()}


@Controller("/ui")
class UiController:
    def __init__(
        self,
        sign_in: SignInHandler,
        sign_out: SignOutHandler,
        current: CurrentSessionHandler,
        sessions: ManageSessionsHandler,
        board: BoardSnapshotHandler,
    ) -> None:
        self._sign_in = sign_in
        self._sign_out = sign_out
        self._current = current
        self._sessions = sessions
        self._board = board

    async def _session(self, request: HttpRequest) -> OwnerSession | None:
        return await self._current.execute(
            CurrentSessionQuery(request.cookies.get(COOKIE))
        )

    async def _checked_post(
        self, request: HttpRequest
    ) -> tuple[OwnerSession | None, dict, HttpResponse | None]:
        """Return the session and the form of a POST, or the response that refuses it."""
        session = await self._session(request)
        if session is None:
            return None, {}, pages.redirect("/ui/login")
        form = await _form(request)
        given = request.headers.get("x-csrf-token") or form.get("csrf")
        if not csrf_matches(given, session.csrf):
            return None, {}, pages.forbidden()
        return session, form, None

    async def _rows(self) -> tuple[list[dict], str]:
        try:
            rows = await self._sessions.execute(ManageSessionsCommand("list"))
            # The action "list" gives a list of session rows.
            assert isinstance(rows, list)
            return list(rows), ""
        except Exception as exc:  # noqa: BLE001
            return [], f"Cannot read sessions: {_error_text(exc)}"

    async def _agents(self) -> list[str]:
        try:
            rows = await self._sessions.execute(ManageSessionsCommand("agents"))
        except Exception:  # noqa: BLE001 - the page shows an empty list, and the error of the rows
            return []
        assert isinstance(rows, list)
        return [r["agent"] for r in rows]

    async def _sessions_page(self, session: OwnerSession, **kwargs) -> HttpResponse:
        rows, error = await self._rows()
        if error and not kwargs.get("error"):
            kwargs["error"] = error
        agents = await self._agents()
        return pages.sessions_page(session.csrf, rows, agents=agents, **kwargs)

    @Get("/style.css")
    async def style(self) -> HttpResponse:
        return pages.style()

    @Get("/")
    async def index(self, request: HttpRequest) -> HttpResponse:
        if await self._session(request):
            return pages.redirect("/ui/sessions")
        return pages.login_page()

    @Get("/login")
    async def login_form(self, request: HttpRequest) -> HttpResponse:
        return await self.index(request)

    @Post("/login")
    async def login(self, request: HttpRequest) -> HttpResponse:
        form = await _form(request)
        result = await self._sign_in.execute(SignInCommand(form.get("credential", "")))
        if result is None:
            return pages.login_page("Sign-in failed.")
        return pages.redirect(
            "/ui/sessions", {"set-cookie": session_cookie(result.token, result.max_age)}
        )

    @Post("/logout")
    async def logout(self, request: HttpRequest) -> HttpResponse:
        session, _, refused = await self._checked_post(request)
        if refused:
            return refused
        assert session is not None
        await self._sign_out.execute(SignOutCommand(session.key))
        return pages.redirect("/ui/login", {"set-cookie": session_cookie("", 0)})

    @Get("/sessions")
    async def sessions(self, request: HttpRequest) -> HttpResponse:
        session = await self._session(request)
        if session is None:
            return pages.redirect("/ui/login")
        return await self._sessions_page(session)

    @Post("/sessions/new")
    async def session_new(self, request: HttpRequest) -> HttpResponse:
        session, form, refused = await self._checked_post(request)
        if refused:
            return refused
        assert session is not None
        try:
            days = int(form.get("days") or 7)
            result = await self._sessions.execute(
                ManageSessionsCommand(
                    "new",
                    agent=form.get("agent", "").strip(),
                    label=form.get("label", "").strip(),
                    days=days,
                )
            )
        except Exception as exc:  # noqa: BLE001
            return await self._sessions_page(session, error=_error_text(exc))
        # The actions "new" and "rotate" give one result object.
        assert isinstance(result, dict)
        return await self._sessions_page(session, secret_result=dict(result))

    @Post("/sessions/rotate")
    async def session_rotate(self, request: HttpRequest) -> HttpResponse:
        session, form, refused = await self._checked_post(request)
        if refused:
            return refused
        assert session is not None
        try:
            result = await self._sessions.execute(
                ManageSessionsCommand("rotate", login=form.get("login", ""))
            )
        except Exception as exc:  # noqa: BLE001
            return await self._sessions_page(session, error=_error_text(exc))
        # The actions "new" and "rotate" give one result object.
        assert isinstance(result, dict)
        return await self._sessions_page(session, secret_result=dict(result))

    @Post("/sessions/revoke")
    async def session_revoke(self, request: HttpRequest) -> HttpResponse:
        session, form, refused = await self._checked_post(request)
        if refused:
            return refused
        assert session is not None
        login = form.get("login", "")
        try:
            await self._sessions.execute(ManageSessionsCommand("revoke", login=login))
        except Exception as exc:  # noqa: BLE001
            return await self._sessions_page(session, error=_error_text(exc))
        return await self._sessions_page(session, notice=f"Revoked {login}.")

    @Get("/board")
    async def board(self, request: HttpRequest) -> HttpResponse:
        session = await self._session(request)
        if session is None:
            return pages.redirect("/ui/login")
        try:
            snapshot = await self._board.execute(BoardSnapshotQuery())
        except Exception as exc:  # noqa: BLE001
            return pages.board_page(session.csrf, None, _error_text(exc))
        return pages.board_page(session.csrf, snapshot)
