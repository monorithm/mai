"""The MCP resource mai://guide: how an agent uses mai. A client can read it with no login.

The resource has only text of the server: the static guide in agent_guide.md, and a table of
the tools that the tool schemas give, thus the table cannot differ from the real tools. It has
no mail text, thus trust rule 3 (mail text only in a tool result) stays true.
"""

from __future__ import annotations

from importlib import resources

from .tool_schemas import TOOLS

GUIDE_URI = "mai://guide"
GUIDE_MIME = "text/markdown"

RESOURCES = (
    {
        "uri": GUIDE_URI,
        "name": "guide",
        "title": "How to use mai",
        "description": "The steps of an agent session: login, mail, threads, the board, errors, and the trust rules.",
        "mimeType": GUIDE_MIME,
    },
)


def _roles(tool: dict) -> str:
    roles = tool.get("roles")
    return "all" if roles in (None, "all") else ", ".join(roles)


def tool_table() -> str:
    lines = ["## Tools", "", "| Tool | Roles | Use |", "|---|---|---|"]
    for t in TOOLS:
        lines.append(f"| `{t['name']}` | {_roles(t)} | {t['description']} |")
    return "\n".join(lines) + "\n"


def guide_text() -> str:
    """The full text of mai://guide."""
    static = (
        resources.files(__package__)
        .joinpath("agent_guide.md")
        .read_text(encoding="utf-8")
    )
    return static.rstrip("\n") + "\n\n" + tool_table()


def read_resource(uri: str) -> dict | None:
    """The contents of one resource, or None for an unknown URI."""
    if uri != GUIDE_URI:
        return None
    return {
        "contents": [{"uri": GUIDE_URI, "mimeType": GUIDE_MIME, "text": guide_text()}]
    }
