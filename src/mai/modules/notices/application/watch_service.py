"""The watch and hook commands: tell a session about new mail.

Exit codes of watch: 0 new mail, 2 login failed or the credential expired or
was revoked, 3 the time limit ended with no mail, 4 the server version changed
since the start of the watch. A lost connection does not end the watch: it
reconnects after a delay that doubles, until the time limit. The hook always
exits 0.

The hook reads stdin: with the credential file "-", the first line is the
credential. The rest is the hook payload of the app (JSON, or empty).
"""

from __future__ import annotations

import sys
import time

import anyio.to_thread
from bustan import Injectable

from mai.modules.credentials import CredentialService
from mai.modules.threads import CredentialReader
from mai.platform.contract import ToolError
from mai.platform.mailserver import MailClientFactory

from ..domain.hook_request import PLAIN, HookRequest
from ..domain.server_version import ServerVersion
from .notice_lines import NOTIFIED, SEARCH, known_set, mailboxes_of, notice_line

EXIT_MAIL = 0
EXIT_LOGIN = 2
EXIT_TIMEOUT = 3
EXIT_SERVER = 4
LOGIN_CODES = ("LOGIN_FAILED", "CREDENTIAL_EXPIRED", "CREDENTIAL_REVOKED")
MAX_BACKOFF = 30.0
# The most notices in the output of one hook for an app. The other mail stays new for the next hook.
HOOK_MAX_NOTICES = 5
MCP_HINT = (
    "mai: to read the mail, call the mai MCP tools list_inbox and read_message"
    " with the ref."
)


def collect(
    mail, mailboxes, known: frozenset[str], limit: int | None = None
) -> tuple[list[str], int]:
    """Give the notice lines of the mail with no notice yet, and the count of the lines not shown.

    Only the mail of the shown lines gets $MaiNotified.
    """
    found: list[tuple[str, int, str]] = []
    for mailbox in mailboxes:
        uids = mail.search(mailbox, SEARCH)
        if not uids:
            continue
        for row in mail.fetch_headers(mailbox, uids):
            line = notice_line(row.get("headers") or {}, known)
            if line:
                found.append((mailbox, row["uid"], line))
    shown = found if limit is None else found[:limit]
    for mailbox in dict.fromkeys(box for box, _, _ in shown):
        uids = [uid for box, uid, _ in shown if box == mailbox]
        mail.store_flags(mailbox, uids, add=(NOTIFIED,))
    return [line for _, _, line in shown], len(found) - len(shown)


def hook_text(lines: list[str], rest: int, fmt: str) -> str:
    """Give the text of a hook: the notice lines, the count of the other mail, and the MCP hint."""
    text = list(lines)
    if rest > 0:
        text.append(f"mai: {rest} more new mail wait. Call list_inbox to see them.")
    if fmt != PLAIN:
        text.append(MCP_HINT)
    return "\n".join(text)


def _is_terminal(stream) -> bool:
    try:
        return bool(stream.isatty())
    except (AttributeError, ValueError, OSError):
        return False


def _close(mail) -> None:
    if mail is not None:
        try:
            mail.close()
        except BaseException:  # noqa: BLE001, S110 - a notice, a hook or a daemon loop must outlive any error
            pass


@Injectable()
class WatchService:
    def __init__(
        self,
        mail_factory: MailClientFactory,
        credentials: CredentialReader,
        version: ServerVersion,
        accounts: CredentialService,
    ) -> None:
        self._mail_factory = mail_factory
        self._credentials = credentials
        self._accounts = accounts
        self.version = version
        self.sleep = time.sleep
        self.stdin = None
        self.out = None

    def _stdin(self):
        return self.stdin or sys.stdin

    def _credential(self, credential_file: str) -> tuple[str, str]:
        """Read the credential from a file, or from stdin when the name is "-"."""
        if credential_file == "-":
            return self._credentials.parse(self._stdin().readline())
        return self._credentials.read(credential_file)

    def _hook_input(self, credential_file: str) -> tuple[str, str, str]:
        """Give the login, the secret and the hook payload. A terminal gives no payload."""
        stream = self._stdin()
        if _is_terminal(stream):
            text = stream.readline() if credential_file == "-" else ""
        else:
            # The bytes of stdin are UTF-8. A bad byte must not stop the hook.
            buffer = getattr(stream, "buffer", None)
            text = (
                buffer.read().decode("utf-8", "replace")
                if buffer is not None
                else stream.read()
            )
        if credential_file == "-":
            first, _, payload = (text or "").partition("\n")
            login, secret = self._credentials.parse(first)
        else:
            payload = text or ""
            login, secret = self._credentials.read(credential_file)
        return login, secret, payload

    async def _known(self) -> frozenset[str]:
        return known_set(await self._accounts.agent_names())

    def _open(self, login: str, secret: str):
        mail = self._mail_factory.open(login, secret)
        mail.connect()
        return mail

    def _print(self, text: str) -> None:
        out = self.out or sys.stdout
        out.write(text + "\n")
        out.flush()

    async def watch(self, credential_file: str, timeout_sec: float = 3000) -> int:
        known = await self._known()
        return await anyio.to_thread.run_sync(
            self._watch, credential_file, timeout_sec, known
        )

    def _watch(self, credential_file: str, timeout_sec: float, known) -> int:
        deadline = time.monotonic() + max(0.0, float(timeout_sec))
        start_version = self.version()
        try:
            login, secret = self._credential(credential_file)
        except ToolError:
            print("mai: login failed (LOGIN_FAILED)", file=sys.stderr)
            return EXIT_LOGIN
        boxes = mailboxes_of(login, known)
        backoff = 0.0
        while True:
            mail = None
            try:
                mail = self._open(login, secret)
                backoff = 0.0
                while True:
                    lines, _ = collect(mail, boxes, known)
                    if lines:
                        self._print("\n".join(lines))
                        return EXIT_MAIL
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        return EXIT_TIMEOUT
                    mail.idle(boxes, remaining)
            except ToolError as exc:
                if exc.code in LOGIN_CODES:
                    print(f"mai: login failed ({exc.code})", file=sys.stderr)
                    return EXIT_LOGIN
            except Exception:  # noqa: BLE001, S110 - a notice, a hook or a daemon loop must outlive any error
                pass
            finally:
                _close(mail)
            # The connection was lost: a new server version ends the watch, else it reconnects.
            now_version = self.version()
            if start_version and now_version and now_version != start_version:
                print("mai: the server version changed", file=sys.stderr)
                return EXIT_SERVER
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                return EXIT_TIMEOUT
            backoff = min(MAX_BACKOFF, max(1.0, backoff * 2))
            self.sleep(min(backoff, remaining))

    def _collect_once(
        self, login: str, secret: str, known: frozenset[str], limit: int | None
    ) -> tuple[list[str], int]:
        mail = None
        try:
            mail = self._open(login, secret)
            return collect(mail, mailboxes_of(login, known), known, limit)
        finally:
            _close(mail)

    async def hook(
        self, credential_file: str, fmt: str = PLAIN, event: str | None = None
    ) -> int:
        """Show the notices of mail with no $MaiNotified, in the format of the app.

        Always exit 0. Print nothing on an error, and when the format, the event or a guard
        selects no notices. The exception is antigravity: it gets "{}" when there are no notices.
        Only the mail of the notices in the output gets $MaiNotified.
        """
        try:
            login, secret, payload = await anyio.to_thread.run_sync(
                self._hook_input, credential_file
            )
            output = ""
            request = HookRequest.parse(fmt, payload, event)
            if request is not None:
                known = await self._known()
                limit = None if fmt == PLAIN else HOOK_MAX_NOTICES
                lines, rest = await anyio.to_thread.run_sync(
                    self._collect_once, login, secret, known, limit
                )
                if lines:
                    output = request.render(hook_text(lines, rest, fmt))
            if not output and fmt == "antigravity":
                output = "{}"  # Antigravity reads a JSON object from each hook.
            if output:
                self._print(output)
        except Exception:  # noqa: BLE001, S110 - a notice, a hook or a daemon loop must outlive any error
            pass
        return 0
