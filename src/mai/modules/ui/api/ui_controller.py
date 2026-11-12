"""The owner UI under /ui.

The UI is off by default. When it is off, each route gives 404. The owner
signs in with a one-time ticket from POST /api/ui/tickets: the page
/ui/sign-in reads the ticket from the fragment and sends it to /ui/session.
That route sets an HttpOnly, SameSite=Strict cookie with Path=/ui. Each POST
of a form must carry the CSRF token of the session. The views are read only
and show no secret.
"""

from __future__ import annotations

import json
import re
from collections.abc import Awaitable, Callable
from urllib.parse import parse_qs, urlsplit

from bustan import Controller, Get, HttpRequest, HttpResponse, Post

from mai.modules.credentials import CredentialService

from ..application.board_snapshot_query import BoardSnapshotHandler, BoardSnapshotQuery
from ..application.current_session_query import (
    CurrentSessionHandler,
    CurrentSessionQuery,
)
from ..application.manage_sessions_command import (
    ManageSessionsCommand,
    ManageSessionsHandler,
)
from ..application.sign_out_command import SignOutCommand, SignOutHandler
from ..application.ui_access_service import UiAccessService
from ..domain.mail_view import (
    COPIES,
    OWNER,
    EnvelopeLookup,
    MailboxMissingError,
    MailReader,
    text_of,
)
from ..domain.owner_session import COOKIE, OwnerSession, csrf_matches, session_cookie
from . import ui_pages as pages

MAX_FORM_BYTES = 16 * 1024
AGENT_NAME = re.compile(r"^[a-z0-9][a-z0-9-]{0,62}$")


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
        access: UiAccessService,
        sign_out: SignOutHandler,
        current: CurrentSessionHandler,
        sessions: ManageSessionsHandler,
        board: BoardSnapshotHandler,
        reader: MailReader,
        envelopes: EnvelopeLookup,
        credentials: CredentialService,
    ) -> None:
        self._access = access
        self._sign_out = sign_out
        self._current = current
        self._sessions = sessions
        self._board = board
        self._reader = reader
        self._envelopes = envelopes
        self._credentials = credentials

    async def _session(self, request: HttpRequest) -> OwnerSession | None:
        return await self._current.execute(
            CurrentSessionQuery(request.cookies.get(COOKIE))
        )

    async def _guarded(
        self,
        request: HttpRequest,
        view: Callable[[OwnerSession], Awaitable[HttpResponse]],
    ) -> HttpResponse:
        """Give 404 when the UI is off, the sign-in note with no session, else the view."""
        if not await self._access.enabled():
            return pages.not_found()
        session = await self._session(request)
        if session is None:
            return pages.signed_out_page()
        return await view(session)

    async def _checked_post(
        self, request: HttpRequest
    ) -> tuple[OwnerSession | None, dict, HttpResponse | None]:
        """Return the session and the form of a POST, or the response that refuses it."""
        if not await self._access.enabled():
            return None, {}, pages.not_found()
        session = await self._session(request)
        if session is None:
            return None, {}, pages.signed_out_page()
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

    async def _mail_list(
        self,
        session: OwnerSession,
        user: str,
        title: str,
        base: str,
        search: str | None,
        missing: str,
        envelopes: bool = False,
    ) -> HttpResponse:
        try:
            rows = await self._reader.messages(user, search or "")
        except MailboxMissingError:
            return pages.mail_list_page(
                session.csrf, title, base, [], search=search, error=missing
            )
        found = None
        if envelopes:
            found = await self._envelopes.find(
                [m.message_id for m in rows if m.message_id]
            )
        return pages.mail_list_page(
            session.csrf, title, base, rows, search=search, envelopes=found
        )

    async def _mail(
        self, session: OwnerSession, user: str, uid: str, title: str, back: str
    ) -> HttpResponse:
        try:
            raw = await self._reader.raw(user, uid)
        except MailboxMissingError:
            raw = None
        if raw is None:
            return pages.not_found()
        mail = text_of(uid, raw)
        if user == COPIES and mail.message_id:
            found = await self._envelopes.find([mail.message_id])
            mail.envelope.update(found.get(mail.message_id, {}))
        return pages.mail_page(session.csrf, title, back, mail)

    async def _known_agent(self, agent: str) -> bool:
        if not AGENT_NAME.match(agent):
            return False
        try:
            return agent in await self._credentials.agent_names()
        except Exception:  # noqa: BLE001 - an unknown list gives no agent view
            return False

    @Get("/style.css")
    async def style(self) -> HttpResponse:
        if not await self._access.enabled():
            return pages.not_found()
        return pages.style()

    @Get("/sign-in")
    async def sign_in(self) -> HttpResponse:
        if not await self._access.enabled():
            return pages.not_found()
        return pages.sign_in_page()

    @Get("/sign-in.js")
    async def sign_in_script(self) -> HttpResponse:
        if not await self._access.enabled():
            return pages.not_found()
        return pages.sign_in_script()

    @Post("/session")
    async def new_session(self, request: HttpRequest) -> HttpResponse:
        if not await self._access.enabled():
            return pages.not_found()
        body = await request.body()
        try:
            data = json.loads(body) if len(body) <= MAX_FORM_BYTES else None
        except ValueError:
            data = None
        ticket = data.get("ticket") if isinstance(data, dict) else None
        result = await self._access.redeem(ticket if isinstance(ticket, str) else "")
        if result is None:
            return HttpResponse.json(
                {"error": {"code": "BAD_TICKET", "message": "the ticket is not valid"}},
                status_code=401,
            )
        response = HttpResponse(status_code=204, body=b"", media_type="text/plain")
        response.headers["set-cookie"] = session_cookie(result.token, result.max_age)
        response.headers["cache-control"] = "no-store"
        return response

    @Get("/")
    async def index(self, request: HttpRequest) -> HttpResponse:
        async def view(_: OwnerSession) -> HttpResponse:
            return pages.redirect("/ui/mail")

        return await self._guarded(request, view)

    @Post("/logout")
    async def logout(self, request: HttpRequest) -> HttpResponse:
        session, _, refused = await self._checked_post(request)
        if refused:
            return refused
        assert session is not None
        await self._sign_out.execute(SignOutCommand(session.key))
        return pages.redirect("/ui/sign-in", {"set-cookie": session_cookie("", 0)})

    @Get("/mail")
    async def all_mail(self, request: HttpRequest) -> HttpResponse:
        search = parse_qs(urlsplit(str(request.url)).query).get("q", [""])[0]

        async def view(s: OwnerSession) -> HttpResponse:
            return await self._mail_list(
                s,
                COPIES,
                "All mail",
                "/ui/mail",
                search,
                "The mailbox copies does not exist. The server makes it in mai 3.0.",
                envelopes=True,
            )

        return await self._guarded(request, view)

    @Get("/mail/{uid}")
    async def one_mail(self, request: HttpRequest, uid: str) -> HttpResponse:
        async def view(s: OwnerSession) -> HttpResponse:
            return await self._mail(s, COPIES, uid, "Mail", "/ui/mail")

        return await self._guarded(request, view)

    @Get("/inbox")
    async def inbox(self, request: HttpRequest) -> HttpResponse:
        async def view(s: OwnerSession) -> HttpResponse:
            return await self._mail_list(
                s, OWNER, "Inbox", "/ui/inbox", None, "Cannot read the owner INBOX."
            )

        return await self._guarded(request, view)

    @Get("/inbox/{uid}")
    async def inbox_mail(self, request: HttpRequest, uid: str) -> HttpResponse:
        async def view(s: OwnerSession) -> HttpResponse:
            return await self._mail(s, OWNER, uid, "Mail", "/ui/inbox")

        return await self._guarded(request, view)

    @Get("/agents")
    async def agents(self, request: HttpRequest) -> HttpResponse:
        async def view(s: OwnerSession) -> HttpResponse:
            try:
                names = await self._credentials.agent_names()
            except Exception as exc:  # noqa: BLE001
                return pages.agents_page(s.csrf, [], _error_text(exc))
            return pages.agents_page(s.csrf, list(names))

        return await self._guarded(request, view)

    @Get("/agents/{agent}")
    async def agent_queue(self, request: HttpRequest, agent: str) -> HttpResponse:
        async def view(s: OwnerSession) -> HttpResponse:
            if not await self._known_agent(agent):
                return pages.not_found()
            return await self._mail_list(
                s,
                agent,
                f"Queue of {agent}",
                f"/ui/agents/{agent}",
                None,
                f"Cannot read the queue of {agent}.",
            )

        return await self._guarded(request, view)

    @Get("/agents/{agent}/{uid}")
    async def agent_mail(
        self, request: HttpRequest, agent: str, uid: str
    ) -> HttpResponse:
        async def view(s: OwnerSession) -> HttpResponse:
            if not await self._known_agent(agent):
                return pages.not_found()
            return await self._mail(s, agent, uid, "Mail", f"/ui/agents/{agent}")

        return await self._guarded(request, view)

    @Get("/threads")
    async def threads(self, request: HttpRequest) -> HttpResponse:
        async def view(s: OwnerSession) -> HttpResponse:
            return pages.threads_page(s.csrf)

        return await self._guarded(request, view)

    @Get("/sessions")
    async def sessions(self, request: HttpRequest) -> HttpResponse:
        return await self._guarded(request, self._sessions_page)

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
        async def view(s: OwnerSession) -> HttpResponse:
            try:
                snapshot = await self._board.execute(BoardSnapshotQuery())
            except Exception as exc:  # noqa: BLE001
                return pages.board_page(s.csrf, None, _error_text(exc))
            return pages.board_page(s.csrf, snapshot)

        return await self._guarded(request, view)
