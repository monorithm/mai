"""The Host and Origin rules of /ui. They are the same rules as the rules of POST /mcp.

The Host header must be in hosts. A missing Origin passes. A present Origin
must be in origins. MAI_HOSTS and MAI_ORIGINS add comma-separated values.
PORT is the port that the server binds.
"""

from __future__ import annotations

import os
from dataclasses import dataclass

DEFAULT_PORT = 8080
PUBLISHED_PORT = 8765


@dataclass(frozen=True)
class UiHttpPolicy:
    hosts: frozenset[str]
    origins: frozenset[str]

    @classmethod
    def from_environment(cls) -> UiHttpPolicy:
        port = os.environ.get("PORT", str(DEFAULT_PORT))
        hosts = {
            f"127.0.0.1:{port}",
            f"localhost:{port}",
            f"127.0.0.1:{PUBLISHED_PORT}",
            f"localhost:{PUBLISHED_PORT}",
        }
        hosts.update(_split(os.environ.get("MAI_HOSTS", "")))
        origins = {f"http://{h}" for h in hosts}
        origins.update(_split(os.environ.get("MAI_ORIGINS", "")))
        return cls(frozenset(hosts), frozenset(origins))

    def admits(self, host: str | None, origin: str | None) -> bool:
        return host in self.hosts and (origin is None or origin in self.origins)


def _split(text: str) -> set[str]:
    return {part.strip() for part in text.split(",") if part.strip()}
