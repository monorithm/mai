"""The hook formats of the apps and their guards."""

import json

import pytest

from mai.modules.notices import HOOK_FORMATS
from mai.modules.notices.domain.hook_request import HookRequest, parse_payload

TEXT = "mai: new mail ref-0123456789abcdef from claude kind request priority high"


def out(fmt, payload=None, event=None):
    text = None if payload is None else json.dumps(payload)
    request = HookRequest.parse(fmt, text, event)
    return None if request is None else request.render(TEXT)


def parsed(fmt, payload=None, event=None):
    result = out(fmt, payload, event)
    return None if result is None else json.loads(result)


def context(event):
    return {"hookSpecificOutput": {"hookEventName": event, "additionalContext": TEXT}}


def test_the_formats():
    assert HOOK_FORMATS == (
        "plain",
        "claude",
        "codex",
        "cursor",
        "copilot",
        "antigravity",
    )


def test_plain_gives_the_text_for_each_event_and_payload():
    assert out("plain") == TEXT
    assert out("plain", {"agent_id": "a1", "hook_event_name": "Stop"}) == TEXT


@pytest.mark.parametrize(
    ("event", "canonical"),
    [
        ("SessionStart", "SessionStart"),
        ("UserPromptSubmit", "UserPromptSubmit"),
        ("PostToolUse", "PostToolUse"),
        ("Stop", "Stop"),
    ],
)
def test_claude_gives_additional_context(event, canonical):
    assert parsed("claude", {"hook_event_name": event}) == context(canonical)


def test_codex_gives_context_and_blocks_at_stop():
    assert parsed("codex", {"hook_event_name": "SessionStart"}) == context(
        "SessionStart"
    )
    assert parsed("codex", {"hook_event_name": "UserPromptSubmit"}) == context(
        "UserPromptSubmit"
    )
    assert parsed("codex", {"hook_event_name": "Stop"}) == {
        "decision": "block",
        "reason": TEXT,
    }
    assert parsed("codex", {"hook_event_name": "PostToolUse"}) is None


def test_cursor_keys():
    assert parsed("cursor", {"hook_event_name": "beforeSubmitPrompt"}) == {
        "additional_context": TEXT
    }
    assert parsed("cursor", {"hook_event_name": "postToolUse"}) == {
        "additional_context": TEXT
    }
    stop = {"hook_event_name": "stop", "status": "completed", "loop_count": 0}
    assert parsed("cursor", stop) == {"followup_message": TEXT}
    # Cursor runs sessionStart for each new chat: it gives nothing.
    assert parsed("cursor", {"hook_event_name": "sessionStart"}) is None


def test_cursor_stop_guards():
    base = {"hook_event_name": "stop", "status": "completed"}
    assert parsed("cursor", {**base, "loop_count": 1}) is None
    assert parsed("cursor", {**base, "status": "aborted"}) is None
    assert parsed("cursor", {"hook_event_name": "stop"}) is None
    # A bool is not a loop count.
    assert parsed("cursor", {**base, "loop_count": True}) == {"followup_message": TEXT}


def test_cursor_guards_and_agent_id():
    event = {"hook_event_name": "postToolUse"}
    # Cursor sends agent_id also for the main agent.
    assert parsed("cursor", {**event, "agent_id": "a1"}) == {"additional_context": TEXT}
    assert parsed("cursor", {**event, "is_background_agent": True}) is None
    assert parsed("cursor", {**event, "parent_tool_call_id": "t1"}) is None


def test_copilot_local_harness():
    assert parsed("copilot", {"hook_event_name": "SessionStart"}) == context(
        "SessionStart"
    )
    # The Local harness gives nothing at the other events.
    for event in ("UserPromptSubmit", "PostToolUse", "Stop"):
        assert parsed("copilot", {"hook_event_name": event}) is None


def test_copilot_harness_has_no_event_name_in_the_payload():
    assert parsed("copilot", {"sessionId": "s1"}, "sessionStart") == {
        "additionalContext": TEXT
    }
    assert parsed("copilot", {"sessionId": "s1"}, "agentStop") == {
        "decision": "block",
        "reason": TEXT,
    }
    assert parsed("copilot", {}, "Stop") == {"decision": "block", "reason": TEXT}
    assert parsed("copilot", {}, "userPromptSubmitted") is None
    assert parsed("copilot", {}, "userPromptSubmit") is None
    assert parsed("copilot", {}, "postToolUse") is None


def test_copilot_local_stop_shape():
    # The Local harness never gets a notice at Stop, but the shape of its Stop output is fixed.
    request = HookRequest("copilot", "Stop", "copilot", {"hook_event_name": "Stop"})
    assert json.loads(request.render(TEXT)) == {
        "hookSpecificOutput": {
            "hookEventName": "Stop",
            "decision": "block",
            "reason": TEXT,
        }
    }


def test_antigravity_injects_a_system_message():
    assert parsed("antigravity", {}, "PreInvocation") == {
        "injectSteps": [{"systemMessage": {"systemMessage": TEXT}}]
    }
    assert parsed("antigravity", {}, "Stop") is None


@pytest.mark.parametrize("fmt", ["claude", "codex", "copilot", "antigravity"])
@pytest.mark.parametrize(
    "key", ["agent_id", "agentId", "parent_agent_id", "parentAgentId"]
)
def test_a_subagent_gets_nothing(fmt, key):
    event = {
        "claude": "SessionStart",
        "codex": "SessionStart",
        "copilot": "sessionStart",
        "antigravity": "PreInvocation",
    }[fmt]
    assert parsed(fmt, {}, event) is not None
    assert parsed(fmt, {key: "x"}, event) is None


def test_a_parent_conversation_is_a_subagent_also_for_cursor():
    event = {"hook_event_name": "postToolUse"}
    assert parsed("cursor", {**event, "parentConversationId": "c1"}) is None
    assert parsed("cursor", {**event, "parent_agent_id": "p1"}) is None


def test_claude_hooks_that_cursor_runs_give_nothing():
    assert parsed("claude", {"hook_event_name": "Stop", "cursor_version": "3"}) is None


@pytest.mark.parametrize("key", ["stop_hook_active", "stopHookActive"])
@pytest.mark.parametrize("fmt", ["claude", "codex"])
def test_an_active_stop_hook_gives_nothing(fmt, key):
    assert parsed(fmt, {"hook_event_name": "Stop", key: True}) is None
    assert parsed(fmt, {"hook_event_name": "Stop", key: False}) is not None
    assert parsed("copilot", {key: True}, "agentStop") is None


def test_the_event_option_comes_before_the_payload():
    assert parsed("claude", {"hook_event_name": "Notification"}, "Stop") == context(
        "Stop"
    )
    assert parsed("claude", {}, None) is None
    assert parsed("claude", {}, "sessionstart") == context("SessionStart")


def test_an_unknown_format_gives_nothing():
    assert parsed("vim", {"hook_event_name": "Stop"}) is None


@pytest.mark.parametrize(
    "text", [None, "", "  \n", "not json", "[1, 2]", "3", "[" * 100000]
)
def test_an_empty_or_bad_payload_is_empty(text):
    assert parse_payload(text) == {}
