"""mai shim install writes the host shims and the compose file of the image to a folder."""

import stat

from mai import app_cli


def _share(tmp_path):
    share = tmp_path / "share"
    share.mkdir()
    for name, text in (
        ("mai", "#!/bin/sh\n"),
        ("mai.ps1", "# ps1\r\n"),
        ("mai.cmd", "@echo off\r\n"),
        ("compose.yaml", "name: mai\n"),
    ):
        (share / name).write_bytes(text.encode())
    return share


def test_install_writes_the_shims_and_the_compose_file(tmp_path, monkeypatch, capsys):
    monkeypatch.setenv("MAI_SHARE", str(_share(tmp_path)))
    out = tmp_path / "out"
    assert app_cli.main(["shim", "install", str(out)]) == 0
    assert (out / "mai").read_bytes() == b"#!/bin/sh\n"
    assert stat.S_IMODE((out / "mai").stat().st_mode) == 0o755
    assert (out / "mai.cmd").read_bytes() == b"@echo off\r\n"
    assert (out / "compose.yaml").read_text() == "name: mai\n"
    assert "wrote:" in capsys.readouterr().out


def test_install_keeps_a_changed_compose_file_unless_force(
    tmp_path, monkeypatch, capsys
):
    monkeypatch.setenv("MAI_SHARE", str(_share(tmp_path)))
    out = tmp_path / "out"
    out.mkdir()
    (out / "compose.yaml").write_text("name: mai\n# changed by the user\n")
    (out / "mai").write_text("an earlier shim\n")
    assert app_cli.main(["shim", "install", str(out)]) == 0
    assert "changed by the user" in (out / "compose.yaml").read_text()
    assert (out / "mai").read_text() == "#!/bin/sh\n"
    assert "kept:" in capsys.readouterr().out
    assert app_cli.main(["shim", "install", str(out), "--force"]) == 0
    assert (out / "compose.yaml").read_text() == "name: mai\n"
