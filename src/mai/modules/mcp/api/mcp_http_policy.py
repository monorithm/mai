"""The guard values of POST /mcp: the Host allowlist, the Origin allowlist, and the body limit."""

import os
from dataclasses import dataclass

MAX_REQUEST = 1024 * 1024
DEFAULT_PORT = 8080
PUBLISHED_PORT = 8765


@dataclass(frozen=True)
class McpHttpPolicy:
    """A missing Origin passes. A present Origin must be in origins. The Host header must be in hosts.

    MAI_HOSTS and MAI_ORIGINS add comma-separated values. PORT is the port that the server binds.
    """

    hosts: frozenset[str]
    origins: frozenset[str]
    max_request: int = MAX_REQUEST

    @classmethod
    def from_environment(cls) -> "McpHttpPolicy":
        port = os.environ.get("PORT", str(DEFAULT_PORT))
        hosts = {
            f"127.0.0.1:{port}",
            f"localhost:{port}",
            f"127.0.0.1:{PUBLISHED_PORT}",
            f"localhost:{PUBLISHED_PORT}",
        }
        hosts.update(_split(os.environ.get("MAI_HOSTS", "")))
        origins = {
            f"http://127.0.0.1:{PUBLISHED_PORT}",
            f"http://localhost:{PUBLISHED_PORT}",
        }
        origins.update(_split(os.environ.get("MAI_ORIGINS", "")))
        return cls(frozenset(hosts), frozenset(origins))

    def admits(self, host: str | None, origin: str | None) -> bool:
        return host in self.hosts and (origin is None or origin in self.origins)


def _split(text: str) -> set[str]:
    return {part.strip() for part in text.split(",") if part.strip()}
