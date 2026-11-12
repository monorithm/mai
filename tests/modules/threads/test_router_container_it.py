"""Container checks of the router, the UI sign-in and watch.

Set MAI_IT and MAI_IT_CONTAINER to a running mai:dev
container that has /data/lanea.cred (cursor), /data/sender.cred (codex)
and /data/w.cred (copilot, no mail).
"""

import os
import pathlib
import subprocess

import pytest

pytestmark = pytest.mark.skipif(
    not os.environ.get("MAI_IT"), reason="needs the mai:dev image"
)
SCRIPTS = pathlib.Path(__file__).parent / "it_scripts"


def _container() -> str:
    return os.environ.get("MAI_IT_CONTAINER", "mai-it")


def _python(script: str) -> str:
    with open(SCRIPTS / script, "rb") as f:
        out = subprocess.run(
            ["docker", "exec", "-i", _container(), "python", "-"],
            stdin=f,
            capture_output=True,
            timeout=60,
            check=False,
        )
    return out.stdout.decode()


def _exec(*args: str) -> int:
    return subprocess.run(
        ["docker", "exec", _container(), *args],
        capture_output=True,
        timeout=30,
        check=False,
    ).returncode


def test_a_reply_goes_to_the_inbox_of_the_claim_holder_in_5_s():
    assert "ROUTED True" in _python("route_reply.py")


def test_the_router_does_not_retry_in_a_loop():
    logs = subprocess.run(
        ["docker", "logs", _container()], capture_output=True, timeout=30, check=False
    )
    text = (logs.stdout + logs.stderr).decode()
    assert text.count("retry in") <= 8


def test_watch_gives_3_without_new_mail():
    assert (
        _exec("mai", "watch", "--credential-file", "/data/w.cred", "--timeout", "3")
        == 3
    )


def test_watch_gives_0_with_new_mail():
    assert (
        _exec(
            "mai",
            "watch",
            "--credential-file",
            "/data/lanea.cred",
            "--timeout",
            "3",
        )
        == 0
    )
