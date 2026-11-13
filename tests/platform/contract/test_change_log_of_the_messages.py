"""The change log tells only the messages that give the rule of the label as session."""

import re
from pathlib import Path

from mai.platform.contract import MESSAGES

ROOT = Path(__file__).resolve().parents[3]
RULE = "Give the label of a named session as session"
SENTENCE = re.compile(
    r"The messages? of ((?:`[A-Z_]+`(?:, |,? and )?)+)\s+also tells? this rule\."
)


# The release that added the rule of the label as session.
RELEASE = "## [3.2.0]"


def release_section() -> str:
    text = (ROOT / "CHANGELOG.md").read_text(encoding="utf-8")
    start = text.index(RELEASE)
    end = text.index("\n## [", start + 1)
    return " ".join(text[start:end].split())


def test_each_message_that_the_change_log_names_tells_the_rule_of_the_label() -> None:
    found = SENTENCE.search(release_section())
    assert found, "the change log names no message that tells the rule"
    codes = re.findall(r"`([A-Z_]+)`", found.group(1))
    assert codes
    for code in codes:
        assert code in MESSAGES, f"{code} has no constant message"
        assert RULE in MESSAGES[code], f"the message of {code} does not tell the rule"
