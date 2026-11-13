"""Each output schema compiles in the strict mode of a JSON Schema validator.

The MCP TypeScript SDK compiles each output schema at listTools. A client can
give the SDK ajv with strict true. In strict mode, ajv refuses a schema object
whose required field is not in the properties of the same schema object
(strictRequired). Then listTools fails, and the client cannot use a tool.
"""

import pytest
from jsonschema import Draft7Validator

from mai.modules.mcp.domain.tool_schemas import TOOLS, public_view


def _schema_objects(schema, at="#"):
    """Each schema object in schema, with its location."""
    if not isinstance(schema, dict):
        return
    yield at, schema
    for name, sub in schema.get("properties", {}).items():
        yield from _schema_objects(sub, f"{at}/properties/{name}")
    for key in ("anyOf", "oneOf", "allOf"):
        for i, sub in enumerate(schema.get(key, ())):
            yield from _schema_objects(sub, f"{at}/{key}/{i}")
    for key in ("items", "additionalProperties", "not"):
        yield from _schema_objects(schema.get(key), f"{at}/{key}")


@pytest.mark.parametrize("tool", TOOLS, ids=lambda t: t["name"])
def test_each_required_field_is_in_the_properties_of_the_same_schema_object(tool):
    schema = public_view(tool)["outputSchema"]
    for at, obj in _schema_objects(schema):
        missing = [
            n for n in obj.get("required", ()) if n not in obj.get("properties", {})
        ]
        assert missing == [], (tool["name"], at, missing)


@pytest.mark.parametrize("tool", TOOLS, ids=lambda t: t["name"])
def test_an_error_with_no_retry_safe_does_not_match(tool):
    schema = tool["outputSchema"]
    if not schema.get("anyOf"):
        pytest.skip("the tool has no alternative for a tool error")
    error = {"error": {"code": "INTERNAL", "message": "The server has an error."}}
    assert not Draft7Validator(schema).is_valid(error)
