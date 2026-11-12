"""The migrations of the board schema. Version range 500 to 599."""

from mai.platform.database import Migration

BOARD_SQL = """
-- The Scrum board: projects, lanes, sprints, items, dependencies, history, evidence, and approvals.
-- Roles of an agent in a project are in mail_auth.membership. A row is never deleted.

CREATE SCHEMA IF NOT EXISTS board;

CREATE TABLE board.project (
  name                  text PRIMARY KEY,
  goal                  text NOT NULL,
  done_rule             jsonb NOT NULL DEFAULT '{"checks": []}'::jsonb,
  repo_path             text,
  wip_limit             int NOT NULL DEFAULT 1 CHECK (wip_limit > 0),
  supervisor_login      text,
  supervisor_lease_end  timestamptz
);

CREATE TABLE board.lane (
  project  text NOT NULL REFERENCES board.project(name),
  name     text NOT NULL,
  agent    text NOT NULL,
  PRIMARY KEY (project, name)
);

CREATE TABLE board.sprint (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  project    text NOT NULL REFERENCES board.project(name),
  goal       text NOT NULL,
  starts_on  date NOT NULL,
  ends_on    date NOT NULL,
  capacity   jsonb NOT NULL DEFAULT '{}'::jsonb,
  started_at timestamptz,
  closed_at  timestamptz,
  retro      text
);

CREATE TABLE board.item (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  ref           text NOT NULL UNIQUE,
  project       text NOT NULL REFERENCES board.project(name),
  key           text NOT NULL,
  title         text NOT NULL,
  spec          jsonb NOT NULL,
  state         text NOT NULL CHECK (state IN ('backlog','ready','assigned','in_progress','blocked',
                                               'in_review','changes_requested','done','cancelled')),
  rank          numeric NOT NULL,
  size          text,
  lane          text,
  assignee      text,
  holder        text,
  reviewer      text,
  sprint        bigint REFERENCES board.sprint(id),
  thread_root   text NOT NULL,
  merge_commit  text,
  last_status   jsonb,
  updated_at    timestamptz NOT NULL DEFAULT now(),
  UNIQUE (project, key)
);
CREATE INDEX item_project_state ON board.item (project, state, rank);

CREATE TABLE board.item_dependency (
  item        bigint NOT NULL REFERENCES board.item(id),
  depends_on  bigint NOT NULL REFERENCES board.item(id),
  PRIMARY KEY (item, depends_on),
  CHECK (item <> depends_on)
);

CREATE TABLE board.item_event (
  seq          bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  item         bigint REFERENCES board.item(id),
  project      text NOT NULL,
  at           timestamptz NOT NULL DEFAULT now(),
  actor        text NOT NULL,
  session      text,
  role         text NOT NULL,
  tool         text NOT NULL,
  from_state   text,
  to_state     text,
  payload      jsonb NOT NULL DEFAULT '{}'::jsonb,
  request_id   text,
  content_sha  text,
  result       jsonb,
  UNIQUE (actor, request_id)
);

CREATE TABLE board.evidence (
  id         bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  item       bigint NOT NULL REFERENCES board.item(id),
  actor      text NOT NULL,
  branch     text NOT NULL,
  commit_sha text NOT NULL,
  body       jsonb NOT NULL,
  at         timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE board.approval (
  id            bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
  project       text NOT NULL REFERENCES board.project(name),
  subject_kind  text NOT NULL CHECK (subject_kind IN ('sprint_goal','scope_change','item','release')),
  subject_id    bigint NOT NULL,
  decision      text NOT NULL,
  note          text,
  at            timestamptz NOT NULL DEFAULT now()
);
"""

MIGRATIONS = [Migration(500, "board", BOARD_SQL)]
