"""The hook formats of the apps, and the guards that decide if a hook shows notices.

Each app runs "mai hook" at some events and gives a JSON payload on stdin. The format selects the
events that show notices and the shape of the output. The plain format prints the notice lines and
has no events and no guards.
"""

from __future__ import annotations

import json
from dataclasses import dataclass

PLAIN = "plain"

# format -> lower-case event name -> (canonical event, output mode).
HOOK_EVENTS: dict[str, dict[str, tuple[str, str]]] = {
    "claude": {
        "sessionstart": ("SessionStart", "context"),
        "userpromptsubmit": ("UserPromptSubmit", "context"),
        "posttooluse": ("PostToolUse", "context"),
        "stop": ("Stop", "context"),
    },
    "codex": {
        "sessionstart": ("SessionStart", "context"),
        "userpromptsubmit": ("UserPromptSubmit", "context"),
        "stop": ("Stop", "block"),
    },
    # beforeSubmitPrompt and postToolUse take additional_context. At stop, followup_message becomes the
    # next user message, thus the stop guards are strict. There is no sessionStart: it runs for each new
    # chat, also a chat that never sends a request, and its context repeats in each request.
    "cursor": {
        "beforesubmitprompt": ("UserPromptSubmit", "additional_context"),
        "posttooluse": ("PostToolUse", "additional_context"),
        "stop": ("Stop", "followup_message"),
    },
    # GitHub Copilot in VS Code. The Local harness sends hook_event_name. The Copilot harness sends a
    # camelCase payload with no hook_event_name, thus the event comes from --event. The output shape
    # follows the harness.
    "copilot": {
        "sessionstart": ("SessionStart", "copilot"),
        "userpromptsubmit": ("UserPromptSubmit", "copilot"),
        "posttooluse": ("PostToolUse", "copilot"),
        "stop": ("Stop", "copilot"),
        "agentstop": ("Stop", "copilot"),
    },
    # PreInvocation runs before each model call, and injectSteps adds a system message. There is no Stop
    # hook, thus mail never makes an Antigravity agent continue by itself.
    "antigravity": {"preinvocation": ("UserPromptSubmit", "antigravity")},
}

# The (harness, canonical event) pairs where a copilot hook shows notices. The Local harness fires
# SessionStart only for the main session at turn 1. A subagent of the Local harness fires the Stop and
# PostToolUse hooks of the main session with the same payload, thus the hook cannot see it there. The
# Copilot harness fires agentStop for the main agent, and it drops the output of userPromptSubmitted.
COPILOT_EVENTS = frozenset(
    {("local", "SessionStart"), ("copilot", "SessionStart"), ("copilot", "Stop")}
)

HOOK_FORMATS = (PLAIN, *HOOK_EVENTS)


def parse_payload(text: str | None) -> dict:
    """Give the JSON object of a hook payload. An empty or bad payload gives {}."""
    text = (text or "").lstrip("\ufeff")
    try:
        data = json.loads(text) if text.strip() else {}
    except (ValueError, RecursionError):  # RecursionError: very deep nesting.
        return {}
    return data if isinstance(data, dict) else {}


def _is_subagent(fmt: str, data: dict) -> bool:
    # Cursor sends agent_id also for the main agent.
    if fmt != "cursor" and (data.get("agent_id") or data.get("agentId")):
        return True
    return bool(
        data.get("parent_agent_id")
        or data.get("parentAgentId")
        or data.get("parentConversationId")
    )


def _stop_allowed(fmt: str, data: dict) -> bool:
    # The turn already continues because of a Stop hook. More mail now could make two agents
    # continue each other without end. The mail stays new for the next hook or the watch command.
    if data.get("stop_hook_active") is True or data.get("stopHookActive") is True:
        return False
    if fmt == "cursor":
        loops = data.get("loop_count")
        if isinstance(loops, int) and not isinstance(loops, bool) and loops > 0:
            return False  # Cursor sends loop_count, not stop_hook_active.
        if data.get("status") != "completed":
            return False  # The user stopped the agent, or the turn failed.
    return True


@dataclass(frozen=True)
class HookRequest:
    """One run of a hook that can show notices: the format, the canonical event, the mode and the payload."""

    fmt: str
    event: str
    mode: str
    payload: dict

    @classmethod
    def parse(
        cls, fmt: str, payload_text: str | None, event: str | None = None
    ) -> HookRequest | None:
        """Give the request, or None when the hook must show nothing (and mark nothing)."""
        data = parse_payload(payload_text)
        if fmt == PLAIN:
            return cls(PLAIN, event or "", PLAIN, data)
        events = HOOK_EVENTS.get(fmt)
        if events is None or _is_subagent(fmt, data):
            return None
        if fmt == "claude" and "cursor_version" in data:
            return None  # Cursor also runs the Claude Code project hooks. They are not for Cursor.
        if fmt == "cursor" and (
            data.get("is_background_agent") is True or data.get("parent_tool_call_id")
        ):
            return None  # A background agent or a subagent (parent_tool_call_id).
        name = event or str(data.get("hook_event_name") or "")
        spec = events.get(name.lower())
        if spec is None:
            return None
        canonical, mode = spec
        if fmt == "copilot" and (cls._harness(data), canonical) not in COPILOT_EVENTS:
            return None
        if canonical == "Stop" and not _stop_allowed(fmt, data):
            return None
        return cls(fmt, canonical, mode, data)

    @staticmethod
    def _harness(data: dict) -> str:
        # The Local harness sends hook_event_name. The Copilot harness does not.
        return "local" if "hook_event_name" in data else "copilot"

    def render(self, text: str) -> str:
        """Give the output for the app: the text for plain, else one JSON object."""
        if self.mode == PLAIN:
            return text
        if self.mode == "context":
            out: dict = {
                "hookSpecificOutput": {
                    "hookEventName": self.event,
                    "additionalContext": text,
                }
            }
        elif self.mode == "block":
            out = {"decision": "block", "reason": text}
        elif self.mode == "antigravity":
            out = {"injectSteps": [{"systemMessage": {"systemMessage": text}}]}
        elif self.mode == "copilot":
            out = self._copilot(text)
        else:
            out = {self.mode: text}
        return json.dumps(out)

    def _copilot(self, text: str) -> dict:
        local = self._harness(self.payload) == "local"
        if self.event == "Stop":
            if local:
                return {
                    "hookSpecificOutput": {
                        "hookEventName": "Stop",
                        "decision": "block",
                        "reason": text,
                    }
                }
            return {"decision": "block", "reason": text}
        if local:
            return {
                "hookSpecificOutput": {
                    "hookEventName": self.event,
                    "additionalContext": text,
                }
            }
        return {"additionalContext": text}
