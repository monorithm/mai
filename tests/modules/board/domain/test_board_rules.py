from typing import Any

import pytest

from mai.modules.board.domain import board_rules as B

RULE = {
    "checks": [
        {"name": "yarn check", "required": True},
        {"name": "yarn test:cov", "required": True, "min_coverage": 80},
        {"name": "route coverage", "required": False},
    ]
}
SHA = "a" * 40


def code(fn, *a):
    with pytest.raises(B.BoardError) as e:
        fn(*a)
    return e.value


def test_transitions_table():
    assert B.check_transition("backlog", "ready", "plan_item") == {"supervisor"}
    assert B.check_transition("ready", "assigned", "assign_item") == {"supervisor"}
    assert "worker" in B.check_transition("assigned", "in_progress", "start_item")
    assert "worker" in B.check_transition(
        "changes_requested", "in_progress", "start_item"
    )
    assert B.check_transition("in_progress", "blocked", "report_status") == {"self"}
    assert B.check_transition("in_review", "done", "review_item") == {
        "supervisor",
        "reviewer",
    }
    assert B.check_transition("in_progress", "cancelled", "plan_item") == {"supervisor"}


@pytest.mark.parametrize(
    "f,t,tool",
    [
        ("backlog", "in_progress", "start_item"),
        ("done", "ready", "plan_item"),
        ("ready", "done", "review_item"),
        ("assigned", "assigned", "assign_item"),
        ("cancelled", "backlog", "plan_item"),
    ],
)
def test_invalid_transition(f, t, tool):
    assert code(B.check_transition, f, t, tool).code == "INVALID_TRANSITION"


def test_roles():
    B.check_role("plan_item", "owner")
    B.check_role("review_item", "reviewer")
    assert code(B.check_role, "plan_item", "worker").code == "FORBIDDEN_ROLE"
    assert code(B.check_role, "start_item", "reviewer").code == "FORBIDDEN_ROLE"


def test_plan_guard():
    B.check_plan({"goal": "g", "acceptance_criteria": ["x"]})
    assert (
        code(B.check_plan, {"goal": "g", "acceptance_criteria": [" "]}).code
        == "INVALID_TRANSITION"
    )
    assert (
        code(B.check_plan, {"acceptance_criteria": ["x"]}).code == "INVALID_TRANSITION"
    )


def test_dependencies_capacity_wip():
    B.check_dependencies({"itm-a": "done"})
    e = code(B.check_dependencies, {"itm-a": "done", "itm-b": "in_review"})
    assert e.code == "DEPENDENCY_OPEN" and e.extra["open"] == ["itm-b"]
    B.check_capacity("a", 1, {"a": 2})
    B.check_capacity("b", 9, {"a": 2})
    assert code(B.check_capacity, "a", 2, {"a": 2}).code == "WIP_LIMIT"
    B.check_wip(0, 1)
    assert code(B.check_wip, 1, 1).code == "WIP_LIMIT"


def test_evidence():
    checks: list[dict[str, Any]] = [
        {"name": "yarn check", "result": "pass"},
        {"name": "yarn test:cov", "result": "pass", "coverage": 85},
    ]
    ev: dict[str, Any] = {"branch": "t/x", "commit": SHA.upper(), "checks": checks}
    out = B.check_evidence(RULE, ev)
    assert out["commit"] == SHA and out["decisions"] == []
    checks[1]["coverage"] = 70
    e = code(B.check_evidence, RULE, ev)
    assert e.code == "EVIDENCE_INCOMPLETE" and e.extra["failed"] == ["yarn test:cov"]
    ev["checks"] = [{"name": "yarn check", "result": "fail"}]
    e = code(B.check_evidence, RULE, ev)
    assert e.extra == {"missing": ["yarn test:cov"], "failed": ["yarn check"]}
    assert (
        code(B.check_evidence, RULE, {"branch": "b", "commit": "abc"}).code
        == "INVALID_ARGUMENT"
    )


def test_review():
    assert B.check_review("accept", {"merge_commit": SHA}) == "done"
    assert B.check_review("changes", {"findings": ["fix x"]}) == "changes_requested"
    assert code(B.check_review, "accept", {}).code == "INVALID_ARGUMENT"
    assert (
        code(B.check_review, "changes", {"findings": [""]}).code == "INVALID_ARGUMENT"
    )


def test_refs_and_retry_key():
    ref = B.new_item_ref()
    assert ref.startswith("itm-") and len(ref) == 16
    assert B.thread_ref_of("<m@mai.internal>").startswith("thr-")
    assert B.content_sha(
        "plan_item", {"a": 1, "request_id": "r1", "session": "s"}
    ) == B.content_sha("plan_item", {"a": 1, "request_id": "r2"})
    assert B.content_sha("plan_item", {"a": 1}) != B.content_sha("plan_item", {"a": 2})


def test_board_mail():
    mid, raw = B.board_mail(
        ["cursor"],
        "[K-1] ready\nx",
        "body\n",
        item_ref="itm-abc",
        project="api",
        in_reply_to="<root@mai.internal>",
    )
    text = raw.decode()
    assert "From: board@mai.internal" in text and "To: cursor@mai.internal" in text
    assert "X-Mai-Kind: notice" in text and "X-Mai-Item: itm-abc" in text
    assert "References: <root@mai.internal>" in text and mid in text
    assert "Subject: [K-1] ready x" in text
