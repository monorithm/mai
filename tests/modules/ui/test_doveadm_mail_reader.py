import json
import subprocess

import anyio
import pytest

from mai.modules.ui.domain.mail_view import MailboxMissingError, text_of
from mai.modules.ui.infra import doveadm_mail_reader as module
from mai.modules.ui.infra.doveadm_mail_reader import DoveadmMailReader


class FakeRun:
    def __init__(self, rows, returncode=0):
        self.rows = rows
        self.returncode = returncode
        self.argv = []

    def __call__(self, argv, **kwargs):
        self.argv.append(argv)
        out = json.dumps(self.rows).encode()
        return subprocess.CompletedProcess(argv, self.returncode, out, b"")


@pytest.fixture
def fake(monkeypatch):
    run = FakeRun(
        [
            {"uid": "2", "hdr.subject": "older", "hdr.from": "a"},
            {"uid": "10", "hdr.subject": "newer", "hdr.from": "b"},
        ]
    )
    monkeypatch.setattr(module.shutil, "which", lambda _: "/usr/bin/doveadm")
    monkeypatch.setattr(module.subprocess, "run", run)
    monkeypatch.setenv("MAI_DOVEADM_SOCKET", "")
    return run


def test_the_list_uses_json_fetch_and_gives_the_newest_first(fake):
    rows = anyio.run(DoveadmMailReader().messages, "copies", "  hello   world ")
    assert [m.uid for m in rows] == ["10", "2"]
    argv = fake.argv[0]
    assert argv[:4] == ["/usr/bin/doveadm", "-f", "json", "fetch"]
    assert argv[argv.index("-u") + 1] == "copies"
    assert argv[-6:] == ["INBOX", "OR", "SUBJECT", "hello world", "FROM", "hello world"]


def test_a_uid_that_is_not_a_number_reads_nothing(fake):
    assert anyio.run(DoveadmMailReader().raw, "copies", "1:*") is None
    assert fake.argv == []


def test_an_error_of_doveadm_means_a_missing_mailbox(monkeypatch):
    monkeypatch.setattr(module.shutil, "which", lambda _: "/usr/bin/doveadm")
    monkeypatch.setattr(module.subprocess, "run", FakeRun([], returncode=68))
    with pytest.raises(MailboxMissingError):
        anyio.run(DoveadmMailReader().messages, "copies", "")


def test_a_mail_with_only_html_gives_a_note_and_no_html():
    raw = b"Subject: x\r\nContent-Type: text/html\r\n\r\n<p>secret html</p>\r\n"
    mail = text_of("1", raw)
    assert "secret html" not in mail.body
    assert "does not show HTML" in mail.body
