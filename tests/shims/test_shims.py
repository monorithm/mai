"""The host shims bin/mai (sh), bin/mai.ps1 (PowerShell) and bin/mai.cmd (Windows), with a fake docker.

Each test runs only where its shell exists: sh on macOS and Linux, PowerShell where pwsh or
powershell is on PATH, and cmd on Windows. CI runs this file on macOS, Linux and Windows.
"""

import json
import os
import shutil
import stat
import subprocess
import sys
from pathlib import Path

import pytest

BIN = Path(__file__).resolve().parents[2] / "bin"
FAKE = Path(__file__).with_name("fake_docker.py")
CRED = "mai1:cursor.lane-a:" + "a" * 52
PWSH = shutil.which("pwsh") or shutil.which("powershell")
ON_WINDOWS = os.name == "nt"


def _fake_docker(tmp_path: Path) -> Path:
    if ON_WINDOWS:
        path = tmp_path / "docker.cmd"
        path.write_text(f'@"{sys.executable}" "{FAKE}" %*\r\n', encoding="ascii")
    else:
        path = tmp_path / "docker"
        path.write_text(
            f'#!/bin/sh\nexec "{sys.executable}" "{FAKE}" "$@"\n', encoding="ascii"
        )
        path.chmod(0o755)
    return path


def _shells():
    found = []
    if not ON_WINDOWS and shutil.which("sh"):
        found.append(("sh", ["sh", str(BIN / "mai")]))
    if PWSH:
        found.append(
            (
                "ps1",
                [
                    PWSH,
                    "-NoLogo",
                    "-NoProfile",
                    "-NonInteractive",
                    "-ExecutionPolicy",
                    "Bypass",
                    "-File",
                    str(BIN / "mai.ps1"),
                ],
            )
        )
    if ON_WINDOWS:
        found.append(("cmd", ["cmd", "/d", "/c", str(BIN / "mai.cmd")]))
    return found


SHELLS = _shells()
pytestmark = pytest.mark.skipif(not SHELLS, reason="no shell for a shim on this OS")


def run(shell, args, tmp_path, stdin="", **env_extra):
    log = tmp_path / "docker.log"
    env = dict(os.environ)
    # The test sets these variables itself, in env_extra.
    env.pop("MAI_CREDENTIAL_FILE", None)
    env.pop("MAI_CREDENTIAL", None)
    env.update(
        {
            "MAI_DOCKER": str(_fake_docker(tmp_path)),
            "FAKE_LOG": str(log),
            "MAI_CONTAINER": "mai",
        },
        **env_extra,
    )
    p = subprocess.run(
        shell + args,
        input=stdin,
        capture_output=True,
        text=True,
        env=env,
        timeout=60,
        check=False,
    )
    calls = (
        [json.loads(line) for line in log.read_text(encoding="utf-8").splitlines()]
        if log.exists()
        else []
    )
    return p, calls


@pytest.fixture(params=SHELLS, ids=[s[0] for s in SHELLS])
def shell(request):
    return request.param[1]


def _cred_file(tmp_path):
    f = tmp_path / "cursor.lane-a.cred"
    f.write_text(CRED + "\n", encoding="ascii")
    return f


def test_a_plain_command_goes_to_the_container_unchanged(shell, tmp_path):
    p, calls = run(
        shell, ["session", "list", "--agent", "cursor"], tmp_path, FAKE_OUT="LOGIN"
    )
    assert p.returncode == 0 and "LOGIN" in p.stdout
    assert calls[0]["argv"] == [
        "exec",
        "-i",
        "mai",
        "mai",
        "session",
        "list",
        "--agent",
        "cursor",
    ]


def test_the_exit_code_of_the_container_comes_back(shell, tmp_path):
    p, _ = run(shell, ["health"], tmp_path, FAKE_RC="3")
    assert p.returncode == 3


def test_stdin_goes_to_the_container_for_mcp(shell, tmp_path):
    line = '{"jsonrpc":"2.0","id":1,"method":"ping"}'
    p, calls = run(shell, ["mcp"], tmp_path, stdin=line + "\n")
    assert p.returncode == 0
    assert calls[0]["argv"] == ["exec", "-i", "mai", "mai", "mcp"]
    assert calls[0]["stdin"].strip() == line


def test_watch_gives_the_credential_on_stdin_and_the_path_stays_on_the_host(
    shell, tmp_path
):
    cred = _cred_file(tmp_path)
    p, calls = run(
        shell,
        ["watch", "--credential-file", str(cred), "--timeout", "3"],
        tmp_path,
        FAKE_RC="3",
    )
    assert p.returncode == 3
    argv = calls[0]["argv"]
    assert argv[:5] == ["exec", "-i", "mai", "mai", "watch"]
    # The place of the option can change between the shims: only the pairs count.
    pairs = " ".join(argv[5:])
    assert (
        "--timeout 3" in pairs
        and "--credential-file -" in pairs
        and str(cred) not in pairs
    )
    assert calls[0]["stdin"].strip() == CRED


def test_watch_with_no_readable_credential_gives_2(shell, tmp_path):
    p, calls = run(
        shell, ["watch", "--credential-file", str(tmp_path / "absent.cred")], tmp_path
    )
    assert p.returncode == 2 and calls == []


def test_a_hook_never_fails_and_drops_the_output_of_an_error(shell, tmp_path):
    cred = _cred_file(tmp_path)
    p, _ = run(
        shell,
        ["hook", "--credential-file", str(cred)],
        tmp_path,
        FAKE_RC="1",
        FAKE_OUT="partial text",
    )
    assert p.returncode == 0 and "partial text" not in p.stdout
    p, calls = run(
        shell, ["hook", "--credential-file", str(tmp_path / "absent.cred")], tmp_path
    )
    assert p.returncode == 0 and p.stdout.strip() == "" and len(calls) == 1


def test_a_hook_shows_the_notice_of_a_good_run(shell, tmp_path):
    cred = _cred_file(tmp_path)
    p, calls = run(
        shell,
        ["hook", "--credential-file", str(cred)],
        tmp_path,
        FAKE_OUT="mai: new mail ref-1",
        FAKE_ERR="a warning\n",
    )
    assert p.returncode == 0 and "mai: new mail ref-1" in p.stdout
    assert calls[0]["stdin"].strip() == CRED


def test_session_new_with_save_writes_only_the_credential_on_the_host(shell, tmp_path):
    target = tmp_path / "keys" / "cursor.lane-b.cred"
    p, calls = run(
        shell,
        ["session", "new", "cursor", "--label", "lane-b", "--save", str(target)],
        tmp_path,
        FAKE_OUT=CRED,
        FAKE_READ_STDIN="0",
    )
    assert p.returncode == 0, p.stderr
    assert calls[0]["argv"] == [
        "exec",
        "-i",
        "mai",
        "mai",
        "session",
        "new",
        "cursor",
        "--label",
        "lane-b",
        "--credential-only",
    ]
    assert target.read_bytes() == (CRED + "\n").encode("ascii")
    assert CRED not in p.stdout
    if not ON_WINDOWS:
        assert stat.S_IMODE(target.stat().st_mode) == 0o600


def test_mcp_with_a_host_credential_file_gives_the_text_in_the_environment(
    shell, tmp_path
):
    cred = _cred_file(tmp_path)
    p, calls = run(shell, ["mcp"], tmp_path, stdin="", MAI_CREDENTIAL_FILE=str(cred))
    assert p.returncode == 0, p.stderr
    argv = calls[0]["argv"]
    assert (
        argv[:2] == ["exec", "-i"]
        and "MAI_CREDENTIAL" in argv
        and argv[-2:] == ["mai", "mcp"]
    )
    assert CRED not in " ".join(argv)
    assert calls[0]["credential"].strip() == CRED


def test_session_rotate_with_save_writes_the_new_credential(shell, tmp_path):
    target = tmp_path / "keys" / "cursor.lane-a.cred"
    p, calls = run(
        shell,
        ["session", "rotate", "cursor.lane-a", "--save", str(target)],
        tmp_path,
        FAKE_OUT=CRED,
        FAKE_READ_STDIN="0",
    )
    assert p.returncode == 0, p.stderr
    assert calls[0]["argv"][-3:] == ["rotate", "cursor.lane-a", "--credential-only"]
    assert target.read_bytes() == (CRED + "\n").encode("ascii")
    if not ON_WINDOWS:
        assert stat.S_IMODE(target.parent.stat().st_mode) == 0o700


def test_a_hook_gives_the_payload_of_the_app_after_the_credential(shell, tmp_path):
    cred = _cred_file(tmp_path)
    payload = json.dumps({"hook_event_name": "Stop", "cwd": "/w/café"})
    p, calls = run(
        shell,
        ["hook", "--credential-file", str(cred), "--format", "claude"],
        tmp_path,
        stdin=payload + "\n",
        FAKE_OUT='{"decision": "block"}',
    )
    assert p.returncode == 0 and '{"decision": "block"}' in p.stdout
    argv = calls[0]["argv"]
    assert argv[:5] == ["exec", "-i", "mai", "mai", "hook"]
    pairs = " ".join(argv[5:])
    assert "--format claude" in pairs and "--credential-file -" in pairs
    first, _, rest = calls[0]["stdin"].partition("\n")
    assert first.strip() == CRED
    assert json.loads(rest) == json.loads(payload)


def test_a_hook_with_no_payload_gives_only_the_credential(shell, tmp_path):
    cred = _cred_file(tmp_path)
    p, calls = run(
        shell, ["hook", "--credential-file", str(cred), "--event", "Stop"], tmp_path
    )
    assert p.returncode == 0
    assert calls[0]["stdin"].strip() == CRED
    assert "--event Stop" in " ".join(calls[0]["argv"][5:])
