import asyncio
import io
import json
import os
import subprocess
from pathlib import Path
from typing import cast

from mai.modules.credentials import CredentialService
from mai.modules.notices.application.notice_lines import (
    known_set,
    mailboxes_of,
    notice_line,
)
from mai.modules.notices.application.watch_service import (
    EXIT_LOGIN,
    EXIT_MAIL,
    EXIT_SERVER,
    EXIT_TIMEOUT,
    HOOK_MAX_NOTICES,
    MCP_HINT,
    WatchService,
)
from mai.modules.notices.domain.server_version import ServerVersion
from mai.modules.threads.infra.credential_file import FileCredentialReader
from mai.platform.contract import ToolError
from mai.platform.mailserver.mail_client_factory import MailClientFactory

from ..threads.fakes import ROOT, FakeMail, headers

SHIM = Path(__file__).resolve().parents[3] / "bin" / "mai"
CRED = "mai1:cursor.lane-a:" + "a" * 26
SENDER = "claude.lead"
KNOWN = known_set(["claude", "cursor"])


def queue(count=3, sender=SENDER):
    """The agent queue of cursor with new mail from a proved sender."""
    box = {
        uid: (headers(f"<m{uid}@mai.internal>", sender=sender), set())
        for uid in range(1, count + 1)
    }
    return {"shared/cursor/INBOX": box}


class Factory:
    def __init__(self, sessions):
        self.sessions = list(sessions)
        self.opened = 0

    def open(self, login, secret):
        self.opened += 1
        item = self.sessions.pop(0)
        if isinstance(item, Exception):
            raise item
        return item


class Versions(ServerVersion):
    """Gives each version in turn, then the last one again on each call."""

    def __init__(self, versions):
        self.seq = list(versions)

    def __call__(self) -> str | None:
        return self.seq.pop(0) if len(self.seq) > 1 else self.seq[0]


class Accounts:
    """A fake of CredentialService: it gives the names of the active agents."""

    def __init__(self, names=("claude", "cursor"), error=None):
        self.names = list(names)
        self.error = error
        self.calls = 0

    async def agent_names(self) -> list[str]:
        self.calls += 1
        if self.error:
            raise self.error
        return sorted(self.names)


def service(sessions, versions=("1",), stdin=CRED + "\n", accounts=None):
    # The fake factory gives fake sessions, not MailSession objects.
    factory = cast(MailClientFactory, Factory(sessions))
    svc = WatchService(
        factory,
        FileCredentialReader(),
        Versions(versions),
        cast(CredentialService, accounts or Accounts()),
    )
    svc.sleep = lambda t: None
    svc.stdin = io.StringIO(stdin)
    svc.out = io.StringIO()
    return svc


def watch(svc, timeout):
    return asyncio.run(svc.watch("-", timeout))


def hook(svc, fmt="plain", event=None, payload=None):
    if payload is not None:
        svc.stdin = io.StringIO(CRED + "\n" + payload)
    return asyncio.run(svc.hook("-", fmt, event))


class Mail(FakeMail):
    def connect(self):
        return self


class LostMail(Mail):
    def idle(self, mailboxes, timeout):
        raise OSError("closed")


def notified(mail):
    box = mail.boxes["shared/cursor/INBOX"]
    return sorted(uid for uid, (_, flags) in box.items() if "$MaiNotified" in flags)


def test_notice_has_only_server_fields():
    line = notice_line(headers(ROOT, sender=SENDER), KNOWN)
    assert line is not None
    assert line.startswith("mai: new mail ref-") and "secret" not in line
    assert "from claude kind request priority high" in line
    assert mailboxes_of("cursor.lane-a", KNOWN) == ["INBOX", "shared/cursor/INBOX"]
    assert mailboxes_of("owner", KNOWN) == ["INBOX"]


def test_the_known_agents_are_data():
    # An agent that is not active is "unknown", and has no agent queue.
    line = notice_line(headers(ROOT, sender="gemini.x"), KNOWN)
    assert line is not None and "from unknown " in line
    assert mailboxes_of("gemini.x", KNOWN) == ["INBOX"]
    assert mailboxes_of("gemini.x", known_set(["gemini"])) == [
        "INBOX",
        "shared/gemini/INBOX",
    ]
    assert "from owner " in (notice_line(headers(ROOT, sender="owner"), KNOWN) or "")
    # There is no alias: "user" is not the owner.
    assert "from unknown " in (notice_line(headers(ROOT, sender="user"), KNOWN) or "")


def test_watch_new_mail_then_marks_notified():
    mail = Mail(queue())
    svc = service([mail])
    assert watch(svc, 5) == EXIT_MAIL
    assert svc.out.getvalue().count("mai: new mail") == 3
    assert "from claude" in svc.out.getvalue()
    assert watch(service([mail]), 0) == EXIT_TIMEOUT


def test_watch_takes_the_agent_names_from_the_credentials_module():
    accounts = Accounts(names=["claude"])
    svc = service([Mail(queue())], accounts=accounts)
    # cursor is not an active agent: the watch does not look in its queue.
    assert watch(svc, 0) == EXIT_TIMEOUT and accounts.calls == 1


def test_watch_login_failed():
    assert watch(service([ToolError("LOGIN_FAILED")]), 5) == EXIT_LOGIN
    assert watch(service([], stdin="bad\n"), 5) == EXIT_LOGIN


def test_lost_connection_reconnects_when_version_is_same():
    svc = service([LostMail({}), Mail(queue())], versions=("1", "1"))
    assert watch(svc, 5) == EXIT_MAIL
    assert svc._mail_factory.opened == 2


def test_exit_4_only_when_version_changed():
    svc = service([LostMail({})], versions=("1", "2"))
    assert watch(svc, 5) == EXIT_SERVER
    # No version source: the watch reconnects and does not exit 4.
    svc = service([LostMail({}), Mail(queue())], versions=(None,))
    assert watch(svc, 5) == EXIT_MAIL


def test_hook_prints_once_and_hides_errors():
    mail = Mail(queue())
    svc = service([mail, mail])
    assert hook(svc) == 0 and "new mail" in svc.out.getvalue()
    # Plain output has no JSON and no MCP hint.
    assert MCP_HINT not in svc.out.getvalue() and not svc.out.getvalue().startswith("{")
    svc2 = service([mail])
    assert hook(svc2) == 0 and svc2.out.getvalue() == ""
    svc3 = service([OSError("down")])
    assert hook(svc3) == 0 and svc3.out.getvalue() == ""
    svc4 = service([Mail(queue())], accounts=Accounts(error=RuntimeError("db")))
    assert hook(svc4) == 0 and svc4.out.getvalue() == ""


def test_hook_with_a_credential_file_reads_the_payload_from_stdin(tmp_path):
    cred = tmp_path / "c.cred"
    cred.write_text(CRED + "\n")
    svc = service([Mail(queue())], stdin='{"hook_event_name": "Stop"}')
    assert asyncio.run(svc.hook(str(cred), "codex")) == 0
    assert json.loads(svc.out.getvalue())["decision"] == "block"


def test_a_json_hook_has_the_notices_and_the_mcp_hint_only():
    mail = Mail(queue())
    svc = service([mail])
    assert hook(svc, "claude", payload='{"hook_event_name": "UserPromptSubmit"}') == 0
    out = json.loads(svc.out.getvalue())
    text = out["hookSpecificOutput"]["additionalContext"]
    assert out["hookSpecificOutput"]["hookEventName"] == "UserPromptSubmit"
    assert text.count("mai: new mail ref-") == 3 and text.endswith(MCP_HINT)
    assert "secret" not in svc.out.getvalue()
    assert notified(mail) == [1, 2, 3]


def test_a_hook_marks_only_the_notices_it_shows():
    mail = Mail(queue(HOOK_MAX_NOTICES + 2))
    svc = service([mail])
    assert hook(svc, "cursor", "postToolUse", payload="") == 0
    text = json.loads(svc.out.getvalue())["additional_context"]
    assert text.count("mai: new mail") == HOOK_MAX_NOTICES
    assert "mai: 2 more new mail wait." in text
    assert notified(mail) == list(range(1, HOOK_MAX_NOTICES + 1))


def test_a_hook_that_shows_nothing_marks_nothing():
    mail = Mail(queue())
    # A subagent, an event that the format does not list, and an unknown format.
    for fmt, event, payload in (
        ("claude", None, '{"hook_event_name": "Stop", "agent_id": "a1"}'),
        ("claude", "Notification", "{}"),
        ("other", "Stop", "{}"),
    ):
        svc = service([mail])
        assert hook(svc, fmt, event, payload) == 0 and svc.out.getvalue() == ""
        assert svc._mail_factory.opened == 0
    assert notified(mail) == []


def test_a_hook_with_a_bad_payload_uses_the_event_option():
    svc = service([Mail(queue())])
    assert hook(svc, "codex", "SessionStart", payload="not json") == 0
    out = json.loads(svc.out.getvalue())
    assert out["hookSpecificOutput"]["hookEventName"] == "SessionStart"


def test_antigravity_gets_an_empty_object_when_there_is_no_notice():
    mail = Mail(queue())
    svc = service([mail])
    assert hook(svc, "antigravity", "PreInvocation", payload="{}") == 0
    out = json.loads(svc.out.getvalue())
    assert out["injectSteps"][0]["systemMessage"]["systemMessage"].count("ref-") == 3
    for event in ("PreInvocation", "Stop"):
        svc = service([mail])
        assert hook(svc, "antigravity", event, payload="{}") == 0
        assert svc.out.getvalue() == "{}\n"
    # An error gives no output, also for antigravity.
    svc = service([OSError("down")])
    assert hook(svc, "antigravity", "PreInvocation", payload="{}") == 0
    assert svc.out.getvalue() == ""


class ByteStdin(io.TextIOWrapper):
    """A stdin with a buffer of bytes, as sys.stdin has."""

    def __init__(self, data: bytes):
        super().__init__(io.BytesIO(data), encoding="ascii")


def test_the_hook_reads_the_bytes_of_stdin_as_utf8():
    # UTF-8 text, and one bad byte in a value.
    payload = b'{"hook_event_name": "Stop", "cwd": "/w/caf\xc3\xa9\xff"}'
    svc = service([Mail(queue())])
    svc.stdin = ByteStdin((CRED + "\n").encode("ascii") + payload)
    assert asyncio.run(svc.hook("-", "codex")) == 0
    assert json.loads(svc.out.getvalue())["decision"] == "block"


def _docker_on_path(tmp_path, code=0):
    bindir = tmp_path / "bin"
    bindir.mkdir()
    d = bindir / "docker"
    d.write_text(f'#!/bin/sh\necho "ARGS:$*"\necho "STDIN:$(cat)"\nexit {code}\n')
    d.chmod(0o755)
    env = {
        k: v
        for k, v in os.environ.items()
        if k not in ("MAI_DOCKER", "MAI_CREDENTIAL_FILE")
    }
    env["PATH"] = f"{bindir}:{env.get('PATH', '/usr/bin:/bin')}"
    env["HOME"] = str(tmp_path)
    return env


def test_shim_watch_passes_credential_on_stdin(tmp_path):
    (tmp_path / ".mai").mkdir()
    (tmp_path / ".mai" / "c.cred").write_text(CRED + "\n")
    env = _docker_on_path(tmp_path)
    r = subprocess.run(
        [
            "sh",
            str(SHIM),
            "watch",
            "--credential-file",
            "~/.mai/c.cred",
            "--timeout",
            "5",
        ],
        capture_output=True,
        check=False,
        text=True,
        env=env,
    )
    assert "ARGS:exec -i mai mai watch --credential-file - --timeout 5" in r.stdout
    assert f"STDIN:{CRED}" in r.stdout


def test_shim_other_commands_and_missing_credential(tmp_path):
    env = _docker_on_path(tmp_path)
    r = subprocess.run(
        ["sh", str(SHIM), "session", "list"],
        capture_output=True,
        check=False,
        text=True,
        env=env,
    )
    assert "ARGS:exec -i mai mai session list" in r.stdout
    r = subprocess.run(
        ["sh", str(SHIM), "watch", "--credential-file", str(tmp_path / "none")],
        capture_output=True,
        check=False,
        text=True,
        env=env,
    )
    assert r.returncode == 2 and "ARGS" not in r.stdout


def test_shim_hook_hides_errors(tmp_path):
    cred = tmp_path / "c.cred"
    cred.write_text(CRED + "\n")
    env = _docker_on_path(tmp_path, code=1)
    r = subprocess.run(
        ["sh", str(SHIM), "hook", f"--credential-file={cred}"],
        capture_output=True,
        check=False,
        text=True,
        env=env,
    )
    assert r.returncode == 0 and r.stdout == ""
    r = subprocess.run(
        ["sh", str(SHIM), "hook", "--credential-file", str(tmp_path / "missing")],
        capture_output=True,
        check=False,
        text=True,
        env=env,
    )
    assert r.returncode == 0 and r.stdout == ""
