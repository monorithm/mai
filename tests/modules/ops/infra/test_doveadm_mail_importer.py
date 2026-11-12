import subprocess

from mai.modules.ops.infra import doveadm_mail_importer as imp


def test_save_and_search_go_to_the_doveadm_server_socket(tmp_path, monkeypatch):
    sock = tmp_path / "doveadm-server"
    sock.touch()
    monkeypatch.setenv("MAI_DOVEADM_SOCKET", str(sock))
    calls = []

    def run(argv, **kw):
        calls.append(argv)
        out = b"guid 4\n" if argv[1] == "search" and len(calls) > 1 else b""
        return subprocess.CompletedProcess(argv, 0, stdout=out)

    monkeypatch.setattr(imp.subprocess, "run", run)
    result = imp.DoveadmRunner().append(
        "cursor", b"raw", ["\\Seen"], "<m@mai.internal>"
    )
    assert result == "imported"
    assert [c[:4] for c in calls] == [
        ["doveadm", "search", "-S", str(sock)],
        ["doveadm", "save", "-S", str(sock)],
        ["doveadm", "search", "-S", str(sock)],
        ["doveadm", "flags", "add", "-S"],
    ]
