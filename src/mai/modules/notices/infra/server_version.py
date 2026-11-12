"""Reads the version of the running server: the file /run/mai/version, else GET /healthz."""

from __future__ import annotations

import json
import os
import urllib.request

from ..domain.server_version import ServerVersion

VERSION_FILE = os.environ.get("MAI_VERSION_FILE", "/run/mai/version")
HEALTH_URL = os.environ.get("MAI_HEALTH_URL", "http://127.0.0.1:8080/healthz")


def server_version(
    version_file: str = VERSION_FILE, health_url: str = HEALTH_URL
) -> str | None:
    """Give the version, or None when no source answers."""
    try:
        with open(version_file, encoding="utf-8") as f:
            value = f.read().strip()
        if value:
            return value
    except OSError:
        pass
    try:
        with urllib.request.urlopen(health_url, timeout=3) as resp:
            value = json.loads(resp.read().decode("utf-8")).get("version")
        return str(value) if value else None
    except Exception:  # noqa: BLE001 - a notice, a hook or a daemon loop must outlive any error
        return None


class FileOrHealthVersion(ServerVersion):
    def __call__(self) -> str | None:
        return server_version()
