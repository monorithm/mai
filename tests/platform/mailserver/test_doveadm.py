from mai.platform.mailserver import doveadm_argv, doveadm_socket


def test_argv_puts_the_socket_after_the_words_of_the_command():
    argv = doveadm_argv(("acl", "set"), ["-u", "cursor", "INBOX"], socket="/run/s")
    assert argv == ["doveadm", "acl", "set", "-S", "/run/s", "-u", "cursor", "INBOX"]


def test_argv_has_no_socket_when_the_socket_does_not_exist(tmp_path, monkeypatch):
    monkeypatch.setenv("MAI_DOVEADM_SOCKET", str(tmp_path / "absent"))
    assert doveadm_socket() is None
    assert doveadm_argv(("save",), ["-u", "cursor"]) == [
        "doveadm",
        "save",
        "-u",
        "cursor",
    ]


def test_argv_uses_the_socket_of_the_environment(tmp_path, monkeypatch):
    sock = tmp_path / "doveadm-server"
    sock.touch()
    monkeypatch.setenv("MAI_DOVEADM_SOCKET", str(sock))
    assert doveadm_argv(("search",), ["-u", "x"], exe="/usr/bin/doveadm") == [
        "/usr/bin/doveadm",
        "search",
        "-S",
        str(sock),
        "-u",
        "x",
    ]


def test_root_uses_a_local_doveadm_with_no_socket(tmp_path, monkeypatch):
    sock = tmp_path / "doveadm-server"
    sock.touch()
    monkeypatch.setenv("MAI_DOVEADM_SOCKET", str(sock))
    monkeypatch.setattr("os.geteuid", lambda: 0)
    assert doveadm_socket() is None
    assert doveadm_argv(("save",), ["-u", "cursor"]) == [
        "doveadm",
        "save",
        "-u",
        "cursor",
    ]
