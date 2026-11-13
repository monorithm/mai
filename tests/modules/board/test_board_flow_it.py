"""The board flow over MCP HTTP against a running mai container.

Set MAI_IT and: MAI_IT_URL (default http://127.0.0.1:8765/mcp), MAI_IT_SUPERVISOR_KEY and
MAI_IT_WORKER_KEY (agent keys of a supervisor and a worker of the project), MAI_IT_PROJECT,
and MAI_IT_LANE (a lane of the worker agent). The done rule of the project must have a required check
"tests" with min_coverage 80.
"""

import json
import os
import urllib.request
import uuid

import pytest

pytestmark = pytest.mark.skipif(not os.environ.get("MAI_IT"), reason="MAI_IT not set")

URL = os.environ.get("MAI_IT_URL", "http://127.0.0.1:8765/mcp")
SHA = "b" * 40


def call(name, arguments):
    # The key "session" of the arguments holds the lease token. It goes in the
    # Authorization header, because the lease of the call gives the session.
    token = arguments.pop("session")
    body = json.dumps(
        {
            "jsonrpc": "2.0",
            "id": 1,
            "method": "tools/call",
            "params": {"name": name, "arguments": arguments},
        }
    ).encode()
    req = urllib.request.Request(
        URL,
        body,
        {
            "Content-Type": "application/json",
            "Accept": "application/json, text/event-stream",
            "Authorization": f"Bearer {token}",
        },
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        result = json.loads(resp.read())["result"]
    return bool(result.get("isError")), result.get("structuredContent") or {}


def lease(agent_key):
    """Get a lease with an agent key. The leases are in the memory of the server, thus
    the test gets them at its start, after the tests that restart the server."""
    req = urllib.request.Request(
        URL.removesuffix("/mcp") + "/api/leases",
        json.dumps({"app": "it", "folder": "/it", "pid": os.getpid()}).encode(),
        {"Content-Type": "application/json", "Authorization": f"Bearer {agent_key}"},
    )
    with urllib.request.urlopen(req, timeout=30) as resp:
        return json.loads(resp.read())["lease"]


def ok(name, arguments):
    failed, out = call(name, arguments)
    assert not failed, out
    return out


def rid():
    return "it-" + uuid.uuid4().hex


def test_plan_assign_start_submit_review():
    project, lane = os.environ["MAI_IT_PROJECT"], os.environ["MAI_IT_LANE"]
    sup = lease(os.environ["MAI_IT_SUPERVISOR_KEY"])
    wrk = lease(os.environ["MAI_IT_WORKER_KEY"])

    planned = ok(
        "plan_item",
        {
            "session": sup,
            "project": project,
            "key": "IT-" + uuid.uuid4().hex[:8],
            "title": "board flow",
            "lane": lane,
            "request_id": rid(),
            "spec": {"goal": "prove the flow", "acceptance_criteria": ["it passes"]},
        },
    )
    assert planned["state"] == "ready" and planned["created"]
    item = planned["item"]

    assert (
        ok(
            "assign_item",
            {
                "session": sup,
                "project": project,
                "item": item,
                "lane": lane,
                "request_id": rid(),
            },
        )["state"]
        == "assigned"
    )
    started = ok(
        "start_item",
        {"session": wrk, "project": project, "item": item, "request_id": rid()},
    )
    assert started["state"] == "in_progress" and started["thread_ref"].startswith(
        "thr-"
    )

    evidence = {
        "branch": "it/flow",
        "commit": SHA,
        "checks": [{"name": "tests", "result": "pass", "coverage": 50}],
    }
    failed, out = call(
        "submit_for_review",
        {
            "session": wrk,
            "project": project,
            "item": item,
            "evidence": evidence,
            "request_id": rid(),
        },
    )
    assert failed and out["error"]["code"] == "EVIDENCE_INCOMPLETE"
    evidence["checks"][0]["coverage"] = 90
    assert (
        ok(
            "submit_for_review",
            {
                "session": wrk,
                "project": project,
                "item": item,
                "evidence": evidence,
                "request_id": rid(),
            },
        )["state"]
        == "in_review"
    )

    reviewed = ok(
        "review_item",
        {
            "session": sup,
            "project": project,
            "item": item,
            "decision": "accept",
            "merge_commit": SHA,
            "request_id": rid(),
        },
    )
    assert reviewed["state"] == "done"
    assert (
        ok("read_item", {"session": sup, "project": project, "item": item})["state"]
        == "done"
    )
