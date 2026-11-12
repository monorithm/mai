"""Runs the milter behind the real Postfix of the mai:dev image."""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = Path(__file__).with_name("postfix_it.sh")

pytestmark = pytest.mark.skipif(not os.environ.get("MAI_IT"), reason="MAI_IT not set")


def test_real_postfix_enforces_from_header():
    out = subprocess.run(
        [
            "docker",
            "run",
            "--rm",
            "--pull=never",
            "--name",
            "mai-test-milter",
            "--entrypoint",
            "sh",
            "-v",
            f"{ROOT / 'src'}:/src:ro",
            "-v",
            f"{SCRIPT}:/it.sh:ro",
            "mai:dev",
            "/it.sh",
        ],
        capture_output=True,
        text=True,
        timeout=120,
        check=False,
    ).stdout
    results = [line for line in out.splitlines() if line.startswith("RESULT")]
    assert results == [
        "RESULT 'cursor@mai.internal' True -> accepted",
        "RESULT 'Cursor Agent <cursor@mai.internal>' True -> accepted",
        (
            "RESULT 'owner@mai.internal' True -> 550 5.7.1 From header rejected: "
            "the From address does not match the authenticated login"
        ),
        (
            "RESULT 'cursor@mai.internal' False -> 550 5.7.1 From header rejected: "
            "no authenticated login"
        ),
    ]
