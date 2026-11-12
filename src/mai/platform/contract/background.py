"""The switch for the background work of the app: the router and the scheduled jobs.

Each process that builds the application runs the bootstrap hooks of bustan:
the HTTP server, each stdio MCP process, and each CLI command. Only one
process must run the router and the jobs, else two routers move the same mail
and each stdio process opens one IMAP connection for each agent queue. The
HTTP server process sets MAI_BACKGROUND=on, and no other process sets it.
"""

from __future__ import annotations

import os

BACKGROUND_ENV = "MAI_BACKGROUND"


def background_on() -> bool:
    """True only in the process that runs the background work."""
    return os.environ.get(BACKGROUND_ENV, "off") == "on"
