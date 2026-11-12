import datetime as dt

from mai.modules.ops.domain.backups import (
    backup_prefix,
    next_backup_at,
    stamp_of,
    stamps_in,
    stamps_to_remove,
)
from mai.modules.ops.domain.maildir import parse_info_flags


def test_parse_flags():
    assert parse_info_flags("x:2,RSTn") == ["\\Answered", "\\Seen", "$MaiNotified"]
    assert parse_info_flags("x:2,") == []
    assert parse_info_flags("x") == []


def test_stamps_and_retention():
    names = [f"mai-2026010{i}T020000Z.pgdump" for i in range(1, 10)]
    names += [f"mai-2026010{i}T020000Z.mail.tar.gz" for i in range(1, 10)] + [
        "other.txt"
    ]
    stamps = stamps_in(names)
    assert stamps[0] == "20260109T020000Z" and len(stamps) == 9
    assert stamps_to_remove(stamps, keep=7) == ["20260102T020000Z", "20260101T020000Z"]
    assert (
        stamp_of(dt.datetime(2026, 1, 2, 3, 4, 5, tzinfo=dt.UTC)) == "20260102T030405Z"
    )
    assert backup_prefix("/b/mai-x.mail.tar.gz") == "/b/mai-x"


def test_next_backup_is_at_two_utc():
    before = dt.datetime(2026, 11, 12, 1, 30, tzinfo=dt.UTC)
    after = dt.datetime(2026, 11, 12, 2, 0, tzinfo=dt.UTC)
    assert next_backup_at(before) == dt.datetime(2026, 11, 12, 2, 0, tzinfo=dt.UTC)
    assert next_backup_at(after) == dt.datetime(2026, 11, 13, 2, 0, tzinfo=dt.UTC)
