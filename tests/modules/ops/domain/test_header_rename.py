import pytest

from mai.modules.ops.domain.maildir import HeaderRenamer, parse_header_rename


def test_a_rename_changes_the_header_prefix_in_the_header_block_only():
    raw = (
        b"From: codex@old.example\r\nX-Old-Kind: request\r\nx-old-priority: high\r\n"
        b"Subject: s\r\n\r\nX-Old-Kind: this line is the body\r\n"
    )
    out = HeaderRenamer([parse_header_rename("X-Old-=X-Mai-")])(raw)
    head, body = out.split(b"\r\n\r\n", 1)
    assert b"X-Mai-Kind: request" in head and b"X-Mai-priority: high" in head
    assert b"X-Old" not in head and b"x-old" not in head
    assert body == b"X-Old-Kind: this line is the body\r\n"


def test_two_renames_apply_in_order_and_no_rename_keeps_the_mail():
    raw = b"X-A-One: 1\nX-B-Two: 2\n\nbody\n"
    renamer = HeaderRenamer([("X-A-", "X-Mai-"), ("X-B-", "X-Mai-")])
    assert renamer(raw) == b"X-Mai-One: 1\nX-Mai-Two: 2\n\nbody\n"
    assert HeaderRenamer()(raw) == raw


@pytest.mark.parametrize("text", ["X-Old-", "=X-Mai-", "X-Old-=", "X Old=X-Mai-"])
def test_a_bad_rename_value_is_an_error(text):
    with pytest.raises(ValueError):
        parse_header_rename(text)
