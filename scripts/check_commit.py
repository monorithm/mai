# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///

"""Examine commit messages against the Conventional Commits rules of the repository.

A message has a header, then an optional body after one blank line, then optional
footers:

    type(scope)!: subject

    Body text that gives the cause of the change.

    BREAKING CHANGE: what a user must change.

Rules:

- type is one of TYPES. The scope is optional: lowercase letters, digits and . _ / -.
  The "!" marks a breaking change. A footer "BREAKING CHANGE: ..." does the same.
- The header has 72 characters or less, and the subject does not end with a period.
- A blank line separates the header and the body.
- The message is ASCII.
- The message has no attribution lines of a tool or an assistant (for example a
  "Co-authored-by" line for Claude, or "Generated with ...").

Git makes merge, revert, fixup! and squash! messages. The check accepts them.

Why: a consistent header lets a reader scan the history by type, and lets a tool make
a change log. The hook stops a commit message that does not obey the rules, before the
commit exists.

Contract: standard library only. Exit codes: 0 when each message obeys the rules, 1
when one does not, 2 for a usage error.

    uv run scripts/check_commit.py .git/COMMIT_EDITMSG
    uv run scripts/check_commit.py --range origin/main..HEAD
    printf 'feat: add the export\\n' | uv run scripts/check_commit.py -

See docs/reference/tooling.md and https://www.conventionalcommits.org/en/v1.0.0/.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

TYPES = (
    "feat",
    "fix",
    "docs",
    "style",
    "refactor",
    "perf",
    "test",
    "build",
    "ci",
    "chore",
)
TYPES += ("revert",)
MAX_HEADER = 72
HEADER = re.compile(
    rf"^(?P<type>{'|'.join(TYPES)})(?:\((?P<scope>[a-z0-9][a-z0-9._/-]*)\))?(?P<bang>!)?: "
    r"(?P<subject>\S.*)$"
)
GIT_MADE = re.compile(r"^(Merge |Revert \"|fixup! |squash! |amend! )")
ATTRIBUTION = re.compile(
    r"^(co-authored-by:.*\b(claude|anthropic|copilot|chatgpt|openai)\b"
    r"|.*\bgenerated (with|by) \[?(claude|copilot|chatgpt)\b)",
    re.IGNORECASE,
)
SCISSORS = "# ------------------------ >8 ------------------------"


def clean(message: str) -> str:
    """The message as git stores it: without the comment lines and the diff part of
    `git commit --verbose`, and without trailing blank lines."""
    message = message.split(SCISSORS, 1)[0]
    lines = [line.rstrip() for line in message.splitlines() if not line.startswith("#")]
    return "\n".join(lines).strip("\n")


def problems(message: str) -> list[str]:
    """The rules that the message does not obey (empty when it obeys each rule)."""
    message = clean(message)
    if not message:
        return ["the message is empty"]
    lines = message.splitlines()
    header = lines[0]
    if GIT_MADE.match(header):
        return []
    found = []
    m = HEADER.match(header)
    if not m:
        found.append(
            f"the header '{header[:80]}' is not 'type(scope): subject' with a type of: "
            + ", ".join(TYPES)
        )
    elif m.group("subject").endswith("."):
        found.append("the subject ends with a period")
    if len(header) > MAX_HEADER:
        found.append(f"the header has {len(header)} characters (maximum {MAX_HEADER})")
    if len(lines) > 1 and lines[1].strip():
        found.append("line 2 is not blank: put one blank line after the header")
    for n, line in enumerate(lines, 1):
        if not line.isascii():
            found.append(f"line {n} is not ASCII")
        if ATTRIBUTION.match(line):
            found.append(f"line {n} is an attribution line: remove it")
    return found


def _range_messages(spec: str) -> list[tuple[str, str]]:
    """(short sha, message) of each commit in a git range, without merge commits."""
    proc = subprocess.run(
        ["git", "log", "--no-merges", "--format=%h%x00%B%x1e", spec],
        capture_output=True,
        text=True,
        check=False,
    )
    if proc.returncode != 0:
        raise SystemExit(
            f"check_commit: git log {spec} did not complete: {proc.stderr.strip()}"
        )
    out = []
    for record in proc.stdout.split("\x1e"):
        if record.strip():
            sha, _, body = record.strip("\n").partition("\0")
            out.append((sha, body))
    return out


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.strip().splitlines()[0])
    group = parser.add_mutually_exclusive_group(required=True)
    group.add_argument("file", nargs="?", help="a commit message file, or - for stdin")
    group.add_argument("--range", help="a git range, for example origin/main..HEAD")
    args = parser.parse_args(argv)

    if args.range:
        messages = _range_messages(args.range)
    elif args.file == "-":
        messages = [("stdin", sys.stdin.read())]
    else:
        messages = [
            (args.file, Path(args.file).read_text(encoding="utf-8", errors="replace"))
        ]

    bad = 0
    for name, message in messages:
        found = problems(message)
        if found:
            bad += 1
            header = clean(message).splitlines()[0] if clean(message) else ""
            print(f"{name}: {header[:72]}")
            for line in found:
                print(f"    {line}")
    if bad:
        print(f"{bad} of {len(messages)} commit messages do not obey the rules.")
        print(
            "Example: 'feat(export): add the ONNX export'. See docs/reference/tooling.md."
        )
        return 1
    if args.range:
        print(f"{len(messages)} commit messages obey the rules")
    return 0


if __name__ == "__main__":
    sys.exit(main())
