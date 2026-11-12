import subprocess

import anyio

from mai.modules.credentials.infra import doveadm_mailbox_acl as acl


def test_grant_sends_acl_set_to_the_doveadm_server_socket(tmp_path, monkeypatch):
    sock = tmp_path / "doveadm-server"
    sock.touch()
    monkeypatch.setenv("MAI_DOVEADM_SOCKET", str(sock))
    monkeypatch.setattr(acl.shutil, "which", lambda name: "/usr/bin/doveadm")
    calls = []
    monkeypatch.setattr(
        acl.subprocess,
        "run",
        lambda argv, **kw: calls.append(argv) or subprocess.CompletedProcess(argv, 0),
    )
    ok = anyio.run(
        acl.DoveadmMailboxAcl().grant, "cursor", "cursor.lane-a", ("lookup", "read")
    )
    assert ok is True
    assert calls == [
        [
            "/usr/bin/doveadm",
            "acl",
            "set",
            "-S",
            str(sock),
            "-u",
            "cursor",
            "INBOX",
            "user=cursor.lane-a",
            "lookup",
            "read",
        ]
    ]


def test_grant_gives_false_with_no_doveadm(monkeypatch):
    monkeypatch.setattr(acl.shutil, "which", lambda name: None)
    assert anyio.run(acl.DoveadmMailboxAcl().remove, "cursor", "cursor.lane-a") is False
