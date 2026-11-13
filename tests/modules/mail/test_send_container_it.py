"""send_message against a running mai container: a failed send, its retry, and the idle timeout of Postfix.

Set MAI_IT and: MAI_IT_CONTAINER (default mai-it), MAI_IT_URL (default
http://127.0.0.1:8765/mcp), MAI_IT_WORKER_KEY (the agent key of cursor), and
MAI_IT_SUPERVISOR_KEY (the agent key of architect). The container has
/data/lanea.cred (a session of cursor). The tests change the internal smtpd
service of Postfix (127.0.0.1:1588) and put it back at the end.
"""

import json
import os
import pathlib
import subprocess
import time
import urllib.request
import uuid

import pytest

pytestmark = pytest.mark.skipif(not os.environ.get("MAI_IT"), reason="MAI_IT not set")

URL = os.environ.get("MAI_IT_URL", "http://127.0.0.1:8765/mcp")
SCRIPTS = pathlib.Path(__file__).parent / "it_scripts"
SERVICE = "127.0.0.1:1588/inet"
SAVED = "/tmp/master.cf.it"


def _container() -> str:
    return os.environ.get("MAI_IT_CONTAINER", "mai-it")


def _exec(*argv: str) -> str:
    out = subprocess.run(
        ["docker", "exec", _container(), *argv],
        capture_output=True,
        text=True,
        timeout=60,
        check=True,
    )
    return out.stdout


def _python(script: str) -> str:
    with open(SCRIPTS / script, "rb") as f:
        out = subprocess.run(
            ["docker", "exec", "-i", _container(), "python", "-"],
            stdin=f,
            capture_output=True,
            timeout=60,
            check=False,
        )
    return out.stdout.decode() + out.stderr.decode()


def _logs(since: str) -> str:
    out = subprocess.run(
        ["docker", "logs", "--since", since, _container()],
        capture_output=True,
        text=True,
        timeout=30,
        check=False,
    )
    return out.stdout + out.stderr


def _postfix_reload() -> None:
    _exec("postfix", "reload")
    # The master opens the port of the service again after the reload.
    _exec(
        "python",
        "-c",
        "import socket, time\n"
        "for _ in range(50):\n"
        "    try:\n"
        "        socket.create_connection(('127.0.0.1', 1588), 1).close()\n"
        "        break\n"
        "    except OSError:\n"
        "        time.sleep(0.2)\n",
    )


def call(token: str, name: str, arguments: dict) -> tuple[bool, dict]:
    body = json.dumps(
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": name, "arguments": arguments},
        }
    ).encode()
    req = urllib.request.Request(
        URL,
        body,
        {
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            "Authorization": f"Bearer {token}",
        },
    )
    with urllib.request.urlopen(req, timeout=60) as resp:
        result = json.loads(resp.read())["result"]
    return bool(result.get("isError")), result.get("structuredContent") or {}


def lease(agent_key: str) -> str:
    """Get a lease. Each lease logs in again, thus it opens a new SMTP connection."""
    req = urllib.request.Request(
        URL.removesuffix("/mcp") + "/api/leases",
        json.dumps({"app": "it", "folder": "/it/send", "pid": os.getpid()}).encode(),
        {"Content-Type": "application/json", "Authorization": f"Bearer {agent_key}"},
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read())["lease"]


def ok(token: str, name: str, arguments: dict) -> dict:
    failed, out = call(token, name, arguments)
    assert not failed, out
    return out


def delivered(token: str, subject: str) -> int:
    """The number of mails with the subject in the queue of the agent architect."""
    listed = ok(token, "list_inbox", {"include_done": True, "limit": 200})
    return sum(1 for m in listed["messages"] if m["subject"] == subject)


def mail(subject: str, request_id: str) -> dict:
    return {
        "to": ["architect"],
        "subject": subject,
        "body": "a test of send_message",
        "request_id": request_id,
    }


def test_the_app_role_frees_the_key_of_a_failed_send():
    results = [
        line
        for line in _python("sent_requests.py").splitlines()
        if line.startswith("RESULT")
    ]
    assert results == [
        "RESULT release done",
        "RESULT retry duplicate=False kept=m2 dated=False",
        "RESULT replay duplicate=True kept=m2 dated=True",
        "RESULT accepted duplicate=True kept=m4 dated=True",
        "RESULT lost duplicate=False kept=m7 dated=False",
    ]


def test_a_retry_after_a_failed_send_delivers_the_mail_once():
    sender = lease(os.environ["MAI_IT_WORKER_KEY"])
    reader = lease(os.environ["MAI_IT_SUPERVISOR_KEY"])
    subject = "it-failed-" + uuid.uuid4().hex[:8]
    args = mail(subject, "it-" + uuid.uuid4().hex)
    _exec("cp", "/etc/postfix/master.cf", SAVED)
    try:
        # Postfix does not take mail on the internal port: remove the service and
        # stop the smtpd processes of its open connections.
        _exec("postconf", "-MX", SERVICE)
        _exec("postfix", "reload")
        # The bracket keeps pkill from a match with the command line of sh.
        _exec("sh", "-c", "pkill -f 'syslog_name=postfix/[i]nternal' || true")
        failed, out = call(sender, "send_message", dict(args))
        assert failed, out
        assert out["error"]["code"] == "MAIL_SERVER_DOWN", out
        assert out["error"]["retry_safe"] is True
    finally:
        _exec("cp", SAVED, "/etc/postfix/master.cf")
        _postfix_reload()
    first = ok(sender, "send_message", dict(args))
    assert first["duplicate"] is False and isinstance(first["date"], str)
    again = ok(sender, "send_message", dict(args))
    assert again["duplicate"] is True and again["message_id"] == first["message_id"]
    assert again["date"] == first["date"]
    deadline = time.monotonic() + 10
    while delivered(reader, subject) < 1 and time.monotonic() < deadline:
        time.sleep(0.5)
    assert delivered(reader, subject) == 1
    ok(sender, "logout", {})
    ok(reader, "logout", {})


def test_a_send_after_the_idle_timeout_of_postfix_delivers_the_mail_once():
    reader = lease(os.environ["MAI_IT_SUPERVISOR_KEY"])
    subject = "it-idle-" + uuid.uuid4().hex[:8]
    _exec("postconf", "-P", f"{SERVICE}/smtpd_timeout=2s")
    _postfix_reload()
    try:
        # The login of the lease opens the SMTP connection. Postfix ends it
        # after 2 s with no command: "timeout after AUTH", then 421.
        since = time.strftime("%Y-%m-%dT%H:%M:%SZ", time.gmtime())
        sender = lease(os.environ["MAI_IT_WORKER_KEY"])
        time.sleep(4)
        assert "timeout after AUTH" in _logs(since)
        out = ok(sender, "send_message", mail(subject, "it-" + uuid.uuid4().hex))
        assert out["duplicate"] is False
    finally:
        _exec("postconf", "-PX", f"{SERVICE}/smtpd_timeout")
        _postfix_reload()
    deadline = time.monotonic() + 10
    while delivered(reader, subject) < 1 and time.monotonic() < deadline:
        time.sleep(0.5)
    assert delivered(reader, subject) == 1
    ok(sender, "logout", {})
    ok(reader, "logout", {})
