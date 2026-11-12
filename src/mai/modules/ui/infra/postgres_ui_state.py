"""Keeps the UI state in the table ui.state of PostgreSQL. Version range 700 to 709."""

from __future__ import annotations

from mai.platform.database import Database, Migration

from ..domain.ui_state import UiStateStore

UI_SQL = """
-- The state of the mail UI. One row; the default is off.
CREATE SCHEMA IF NOT EXISTS ui;

CREATE TABLE ui.state (
  id          int PRIMARY KEY DEFAULT 1 CHECK (id = 1),
  enabled     boolean NOT NULL DEFAULT false,
  updated_at  timestamptz NOT NULL DEFAULT now()
);
INSERT INTO ui.state (id, enabled) VALUES (1, false);
"""

MIGRATIONS = [Migration(700, "ui_state", UI_SQL)]


class PostgresUiStateStore(UiStateStore):
    def __init__(self, database: Database) -> None:
        self._database = database

    async def enabled(self) -> bool:
        row = await self._database.fetch_one(
            "SELECT enabled FROM ui.state WHERE id = 1"
        )
        return bool(row and row[0])

    async def set_enabled(self, enabled: bool) -> None:
        async with self._database.connection() as conn:
            await conn.execute(
                "INSERT INTO ui.state (id, enabled, updated_at) VALUES (1, %s, now()) "
                "ON CONFLICT (id) DO UPDATE SET enabled = EXCLUDED.enabled, "
                "updated_at = now()",
                (enabled,),
            )
