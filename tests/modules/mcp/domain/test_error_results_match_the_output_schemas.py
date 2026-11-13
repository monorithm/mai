"""Each tool error matches the output schema of its tool.

A strict MCP client compares the structured content of each tool result with
the output schema of the tool, also for an error result. The client refuses a
result that does not match, and the chat then sees the client error -32602 in
place of the code of the error. The bridge makes its tool errors in the same
form as the server.
"""

import pytest
from jsonschema import Draft7Validator, Draft202012Validator

from mai.modules.mcp.application.json_rpc import error_result, validate
from mai.modules.mcp.domain.tool_schemas import BY_NAME, TOOLS
from mai.platform.contract import MESSAGES, ToolError

VALIDATORS = [Draft7Validator, Draft202012Validator]
# The tool errors that only the bridge gives, in the form of the bridge.
BRIDGE_ERRORS = [
    {
        "error": {
            "code": "SERVER_UNAVAILABLE",
            "message": "The server does not reply. Try again later.",
            "path": "session",
            "retry_safe": True,
        }
    },
    {
        "error": {
            "code": "SESSION_UNKNOWN",
            "message": "The session handle is unknown, expired, or ended.",
            "path": "session",
            "retry_safe": False,
        }
    },
]


def server_errors() -> list[dict]:
    errors = [error_result(ToolError(code))["structuredContent"] for code in MESSAGES]
    errors.append(
        error_result(ToolError("UNKNOWN_MESSAGE", path="ref"))["structuredContent"]
    )
    errors.append(
        error_result(
            ToolError("RECIPIENT_UNKNOWN", path="to", refused=["nobody@mai.internal"])
        )["structuredContent"]
    )
    return errors


@pytest.mark.parametrize("validator", VALIDATORS, ids=lambda v: v.__name__)
@pytest.mark.parametrize("tool", TOOLS, ids=lambda t: t["name"])
def test_each_tool_error_matches_the_output_schema_of_the_tool(tool, validator):
    schema = tool["outputSchema"]
    validator.check_schema(schema)
    check = validator(schema)
    for error in server_errors() + BRIDGE_ERRORS:
        problems = [p.message for p in check.iter_errors(error)]
        assert problems == [], (tool["name"], error["error"]["code"], problems)


@pytest.mark.parametrize("tool", TOOLS, ids=lambda t: t["name"])
def test_a_result_with_no_required_field_and_no_error_does_not_match(tool):
    schema = tool["outputSchema"]
    if not schema.get("anyOf") and not schema.get("required"):
        pytest.skip("the tool has no required output field")
    assert not Draft7Validator(schema).is_valid({})


def test_the_validator_of_the_server_applies_the_two_alternatives():
    schema = BY_NAME["reply"]["outputSchema"]
    validate(
        schema,
        error_result(ToolError("UNKNOWN_MESSAGE", path="ref"))["structuredContent"],
    )
    validate(
        schema,
        {
            "ref": "ref-0123456789abcdef",
            "thread_ref": "thr-0123456789abcdef",
            "message_id": "<msg-1@mai.internal>",
            "duplicate": False,
        },
    )
    with pytest.raises(ToolError):
        validate(schema, {"ref": "ref-0123456789abcdef"})
