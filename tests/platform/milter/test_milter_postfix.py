"""Runs the milter behind the real Postfix of the mai image.

MAI_IT_IMAGE sets the image (default mai:dev). The name of the test container
starts with MAI_IT_CONTAINER (default mai-test), thus two runs with different
names do not collide.
"""

from __future__ import annotations

import os
import subprocess
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[3]
SCRIPT = Path(__file__).with_name("postfix_it.sh")
IMAGE = os.environ.get("MAI_IT_IMAGE") or "mai:dev"
NAME = (os.environ.get("MAI_IT_CONTAINER") or "mai-test") + "-milter"

pytestmark = pytest.mark.skipif(not os.environ.get("MAI_IT"), reason="MAI_IT not set")


def test_real_postfix_enforces_from_header():
    out = subprocess.run(
        [
            "docker",
            "run",
            "--rm",
            "--pull=never",
            "--name",
            NAME,
            "--entrypoint",
            "sh",
            "-v",
            f"{ROOT / 'src'}:/src:ro",
            "-v",
            f"{SCRIPT}:/it.sh:ro",
            IMAGE,
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
