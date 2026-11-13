from bustan import ClassProvider, FactoryProvider, Module

from mai.platform.database import Database, DatabaseModule

from .api.agents_controller import AgentsController
from .application.add_agent_command import AddAgentHandler
from .application.agent_keys import AgentKeys
from .application.authenticate_command import AuthenticateHandler
from .application.check_login_query import CheckLoginHandler
from .application.credential_service import CredentialService
from .application.get_account_kind_query import GetAccountKindHandler
from .application.get_roles_query import GetRolesHandler
from .application.grant_mailbox_access_command import GrantMailboxAccessHandler
from .application.init_accounts_command import InitAccountsHandler
from .application.list_agents_query import ListAgentsHandler
from .application.list_sessions_query import ListSessionsHandler
from .application.new_session_command import NewSessionHandler
from .application.revoke_session_command import RevokeSessionHandler
from .application.rotate_session_command import RotateSessionHandler
from .application.set_owner_key_command import SetOwnerKeyHandler
from .domain.agent_key_store import AgentKeyStore
from .domain.credential_files import CredentialFiles
from .domain.credential_store import CredentialStore
from .domain.mailbox_acl import MailboxAcl
from .domain.secret_hasher import SecretHasher
from .infra.bcrypt_secret_hasher import BcryptSecretHasher
from .infra.doveadm_mailbox_acl import DoveadmMailboxAcl
from .infra.in_memory_agent_key_store import InMemoryAgentKeyStore
from .infra.in_memory_credential_store import InMemoryCredentialStore
from .infra.local_credential_files import LocalCredentialFiles
from .infra.postgres_agent_key_store import PostgresAgentKeyStore
from .infra.postgres_credential_store import PostgresCredentialStore


def is_postgres(database: Database) -> bool:
    """True when DATABASE_BACKEND selects PostgreSQL for this Database."""
    return bool(database.is_postgres)


def _credential_store(database: Database) -> CredentialStore:
    if is_postgres(database):
        return PostgresCredentialStore(database)
    return InMemoryCredentialStore()


def _agent_key_store(database: Database) -> AgentKeyStore:
    if is_postgres(database):
        return PostgresAgentKeyStore(database)
    return InMemoryAgentKeyStore()


# The module binds each port to an adapter. DATABASE_BACKEND selects the
# adapter of the store. The migrations of the store run in app_migrate before
# the daemons start, because Dovecot and Postfix read the same tables.
@Module(
    imports=[DatabaseModule],
    controllers=[AgentsController],
    providers=[
        FactoryProvider(
            provide=CredentialStore, use_factory=_credential_store, inject=(Database,)
        ),
        FactoryProvider(
            provide=AgentKeyStore, use_factory=_agent_key_store, inject=(Database,)
        ),
        ClassProvider(provide=SecretHasher, use_class=BcryptSecretHasher),
        ClassProvider(provide=MailboxAcl, use_class=DoveadmMailboxAcl),
        ClassProvider(provide=CredentialFiles, use_class=LocalCredentialFiles),
        InitAccountsHandler,
        NewSessionHandler,
        ListSessionsHandler,
        RotateSessionHandler,
        RevokeSessionHandler,
        AuthenticateHandler,
        CheckLoginHandler,
        GetRolesHandler,
        GrantMailboxAccessHandler,
        AddAgentHandler,
        ListAgentsHandler,
        GetAccountKindHandler,
        SetOwnerKeyHandler,
        CredentialService,
        AgentKeys,
    ],
    exports=[CredentialService, AgentKeys],
)
class CredentialsModule:
    pass
