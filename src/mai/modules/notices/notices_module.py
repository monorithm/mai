from bustan import ClassProvider, Module

from mai.modules.credentials import CredentialsModule
from mai.modules.threads import ThreadsModule
from mai.platform.mailserver import MailServerModule

from .application.watch_service import WatchService
from .domain.server_version import ServerVersion
from .infra.server_version import FileOrHealthVersion


# ThreadsModule gives the CredentialReader. CredentialsModule gives the names of
# the active agents, for the sender of a notice and the agent queue. ServerVersion reads
# /run/mai/version, else GET /healthz, for the exit code 4 of watch.
@Module(
    imports=[MailServerModule, ThreadsModule, CredentialsModule],
    providers=[
        ClassProvider(provide=ServerVersion, use_class=FileOrHealthVersion),
        WatchService,
    ],
    exports=[WatchService],
)
class NoticesModule:
    pass
