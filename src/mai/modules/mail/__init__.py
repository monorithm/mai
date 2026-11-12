"""The mail module: the MCP mail tools (send, reply, list, read, wait, done) over the mail session of each login."""

from .api.mail_tools import MailTools
from .infra.postgres_mail_store import MIGRATIONS
from .mail_module import MailModule

__all__ = ["MIGRATIONS", "MailModule", "MailTools"]
