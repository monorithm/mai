from bustan import ClassProvider, FactoryProvider, Module

from mai.modules.sessions import SessionsModule
from mai.modules.threads import ThreadsModule
from mai.platform.database import Database, DatabaseModule

from .api.mail_tools import MailTools
from .application.list_addresses_query import ListAddressesHandler
from .application.list_inbox_query import ListInboxHandler
from .application.mail_reader import MailReader
from .application.mail_sender import MailSender
from .application.mark_done_command import MarkDoneHandler
from .application.read_message_query import ReadMessageHandler
from .application.read_thread_query import ReadThreadHandler
from .application.reply_command import ReplyHandler
from .application.send_message_command import SendMessageHandler
from .application.wait_for_mail_query import WaitForMailHandler, WaitSlots
from .domain.ports import Directory, MailboxAccess, SentRequests
from .infra.memory_directory import InMemoryDirectory
from .infra.memory_sent_requests import InMemorySentRequests
from .infra.postgres_mail_store import PostgresDirectory, PostgresSentRequests
from .infra.threaded_mailbox_access import ThreadedMailboxAccess


def _sent_requests(database: Database) -> SentRequests:
    if database.is_postgres:
        return PostgresSentRequests(database)
    return InMemorySentRequests()


def _directory(database: Database) -> Directory:
    if database.is_postgres:
        return PostgresDirectory(database)
    return InMemoryDirectory()


# The module binds the ports of the domain to the adapters that
# DATABASE_BACKEND selects. The mail session of each login is the
# synchronous MailSession of the platform; ThreadedMailboxAccess calls it in
# a worker thread. ReplyHandler claims a queue thread through the
# ClaimService of ThreadsModule before it sends. The tables of this module
# are in MIGRATIONS, which app_migrate applies before the daemons start.
@Module(
    imports=[DatabaseModule, SessionsModule, ThreadsModule],
    providers=[
        FactoryProvider(
            provide=SentRequests, use_factory=_sent_requests, inject=(Database,)
        ),
        FactoryProvider(provide=Directory, use_factory=_directory, inject=(Database,)),
        ClassProvider(provide=MailboxAccess, use_class=ThreadedMailboxAccess),
        MailReader,
        MailSender,
        WaitSlots,
        ListAddressesHandler,
        SendMessageHandler,
        ReplyHandler,
        ListInboxHandler,
        ReadMessageHandler,
        ReadThreadHandler,
        WaitForMailHandler,
        MarkDoneHandler,
        MailTools,
    ],
    exports=[MailTools],
)
class MailModule:
    pass
