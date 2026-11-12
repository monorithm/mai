# /// script
# requires-python = ">=3.12"
# dependencies = []
# ///

"""Find access tokens, private keys and secret files before they reach git.

The scan looks for the forms of frequent tokens (Hugging Face, GitHub, OpenAI and
Anthropic, AWS, Google, Slack) and for private key blocks. It also refuses the files
.env and .env.* (except .env.example) and private key files.

Three modes:

- FILES: the given files, for example the staged files of the pre-commit hook.
- --all: each file that git tracks, plus the untracked files that git does not
  ignore. Use it in CI.
- --history: each change in each commit of each branch. Use it before the first
  push to a new remote: a token in an old commit stays in the history.

The output gives the file, the line and the kind of secret, never the value. A line
that contains the text "secret-scan: ignore" is not examined (for a test fixture with
a fake token).

Why: a token in a pushed commit is public for each reader of the repository, and a
removal from the last commit does not remove it from the history. Revoke a found token
at its provider, also after you remove it from the files.

Contract: standard library only. Exit codes: 0 when nothing is found, 1 when a secret
or a secret file is found, 2 for a usage error.

    uv run scripts/check_secrets.py src/pkg/hub.py configs/hub.yaml
    uv run scripts/check_secrets.py --all
    uv run scripts/check_secrets.py --history

See docs/reference/tooling.md.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path

ROOT = Path(__file__).resolve().parents[1]
PATTERNS = {
    "Hugging Face token": re.compile(r"\bhf_[A-Za-z0-9]{30,}\b"),
    "GitHub token": re.compile(
        r"\b(?:gh[pousr]_[A-Za-z0-9]{36,}|github_pat_[A-Za-z0-9_]{60,})"
    ),
    "OpenAI or Anthropic key": re.compile(r"\bsk-(?:ant-|proj-)?[A-Za-z0-9_-]{32,}"),
    "AWS access key id": re.compile(r"\b(?:AKIA|ASIA)[0-9A-Z]{16}\b"),
    "Google API key": re.compile(r"\bAIza[0-9A-Za-z_-]{35}\b"),
    "Slack token": re.compile(r"\bxox[abprs]-[A-Za-z0-9-]{10,}"),
    "private key block": re.compile(r"-----BEGIN [A-Z ]*PRIVATE KEY-----"),
}
IGNORE_MARK = "secret-scan: ignore"
SECRET_FILE = re.compile(
    r"(^|/)(\.env(\.[\w.-]+)?|id_(rsa|dsa|ecdsa|ed25519)|[^/]+\.(pem|key|p12))$"
)
ALLOWED_FILES = re.compile(r"(^|/)\.env\.example$")
BINARY = {
    ".png",
    ".jpg",
    ".jpeg",
    ".gif",
    ".pdf",
    ".zip",
    ".gz",
    ".onnx",
    ".pt",
    ".npz",
}


def scan_text(text: str) -> list[tuple[int, str]]:
    """(line, kind) of each secret in a text."""
    found = []
    for n, line in enumerate(text.splitlines(), 1):
        if IGNORE_MARK in line:
            continue
        found += [
            (n, kind) for kind, pattern in PATTERNS.items() if pattern.search(line)
        ]
    return found


def secret_file(name: str) -> bool:
    """True for a file name that must never be in git (.env, a private key file)."""
    return bool(SECRET_FILE.search(name)) and not ALLOWED_FILES.search(name)


def _git(*args: str) -> str:
    proc = subprocess.run(
        ["git", *args], cwd=ROOT, capture_output=True, text=True, check=False
    )
    if proc.returncode != 0:
        raise SystemExit(
            f"check_secrets: git {args[0]} did not complete: {proc.stderr.strip()}"
        )
    return proc.stdout


def scan_files(names: list[str]) -> list[str]:
    found = []
    for name in names:
        path = Path(name) if Path(name).is_absolute() else ROOT / name
        if secret_file(name):
            found.append(f"{name}: a secret file; keep it out of git (.gitignore)")
        if not path.is_file() or path.suffix.lower() in BINARY:
            continue
        data = path.read_bytes()
        if b"\0" in data:
            continue
        for line, kind in scan_text(data.decode("utf-8", "replace")):
            found.append(f"{name}:{line}: {kind}")
    return found


def scan_history() -> list[str]:
    """The secrets in the added lines of each commit of each branch, and secret files."""
    found = []
    log = _git(
        "log", "--all", "-p", "--no-color", "--no-ext-diff", "--format=commit %H"
    )
    commit = path = ""
    for line in log.splitlines():
        if line.startswith("commit "):
            commit = line.split()[1][:12]
        elif line.startswith("+++ b/"):
            path = line[6:]
            if secret_file(path):
                found.append(f"{commit} {path}: a secret file in the history")
        elif line.startswith("+") and not line.startswith("+++"):
            for _, kind in scan_text(line[1:]):
                found.append(f"{commit} {path}: {kind}")
    return sorted(set(found))


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.strip().splitlines()[0])
    parser.add_argument(
        "files", nargs="*", help="files to scan (paths from the repository root)"
    )
    mode = parser.add_mutually_exclusive_group()
    mode.add_argument(
        "--all", action="store_true", help="scan each file that git does not ignore"
    )
    mode.add_argument(
        "--history", action="store_true", help="scan each commit of each branch"
    )
    args = parser.parse_args(argv)
    if (args.all or args.history) and args.files:
        parser.error("give files, or --all, or --history")

    if args.history:
        found = scan_history()
    elif args.all:
        names = _git(
            "ls-files", "--cached", "--others", "--exclude-standard"
        ).splitlines()
        found = scan_files(names)
    else:
        found = scan_files(args.files)
    for line in found:
        print(line)
    if found:
        print(
            f"check_secrets: {len(found)} finding(s). Remove each secret, and revoke a real token."
        )
        return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
