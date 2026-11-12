from bustan import Injectable

from mai.platform.contract import ToolError

from ..domain.credential import parse_credential
from ..domain.credential_files import CredentialFiles
from ..domain.errors import CredentialError
from .add_agent_command import AddAgentCommand, AddAgentHandler
from .authenticate_command import AuthenticateCommand, AuthenticateHandler
from .check_login_query import CheckLoginHandler, CheckLoginQuery
from .get_account_kind_query import GetAccountKindHandler, GetAccountKindQuery
from .get_roles_query import GetRolesHandler, GetRolesQuery
from .grant_mailbox_access_command import (
    GrantMailboxAccessCommand,
    GrantMailboxAccessHandler,
)
from .init_accounts_command import InitAccountsCommand, InitAccountsHandler
from .list_agents_query import ListAgentsHandler, ListAgentsQuery
from .list_sessions_query import ListSessionsHandler, ListSessionsQuery
from .new_session_command import NewSessionCommand, NewSessionHandler
from .revoke_session_command import RevokeSessionCommand, RevokeSessionHandler
from .rotate_session_command import RotateSessionCommand, RotateSessionHandler
from .set_owner_key_command import SetOwnerKeyCommand, SetOwnerKeyHandler


@Injectable()
class CredentialService:
    """The exported face of the credentials module for other modules and the CLI.

    A failed login check raises ToolError with the code of the tool contract.
    The owner commands raise ValueError with a plain message.
    """

    def __init__(
        self,
        init_accounts: InitAccountsHandler,
        new_session: NewSessionHandler,
        list_sessions: ListSessionsHandler,
        rotate_session: RotateSessionHandler,
        revoke_session: RevokeSessionHandler,
        authenticate: AuthenticateHandler,
        check_login: CheckLoginHandler,
        get_roles: GetRolesHandler,
        files: CredentialFiles,
        grant_mailbox_access: GrantMailboxAccessHandler,
        add_agent: AddAgentHandler,
        list_agents: ListAgentsHandler,
        account_kind: GetAccountKindHandler,
        set_owner_key: SetOwnerKeyHandler,
    ) -> None:
        self._init_accounts = init_accounts
        self._new_session = new_session
        self._list_sessions = list_sessions
        self._rotate_session = rotate_session
        self._revoke_session = revoke_session
        self._authenticate = authenticate
        self._check_login = check_login
        self._get_roles = get_roles
        self._files = files
        self._grant_mailbox_access = grant_mailbox_access
        self._add_agent = add_agent
        self._list_agents = list_agents
        self._account_kind = account_kind
        self._set_owner_key = set_owner_key

    async def init_accounts(self, write_secrets: bool = True) -> dict:
        return await self._init_accounts.execute(
            InitAccountsCommand(write_secrets=write_secrets)
        )

    async def owner_set_key(self, key: str, replace: bool = False) -> dict:
        """Store the hash of the owner key. Raises AccountStateError when a key exists."""
        return await self._set_owner_key.execute(
            SetOwnerKeyCommand(key=key, replace=replace)
        )

    async def grant_mailbox_access(self) -> dict:
        """Give the board and each live session its rights on the agent queues."""
        return await self._grant_mailbox_access.execute(GrantMailboxAccessCommand())

    async def agent_add(self, name: str) -> dict:
        """Make the agent account NAME. Raises ValueError for a bad or taken name."""
        return await self._add_agent.execute(AddAgentCommand(name=name))

    async def agent_list(self) -> list[dict]:
        """The agent accounts with their status and the count of live sessions."""
        return await self._list_agents.execute(ListAgentsQuery())

    async def agent_names(self) -> list[str]:
        """The logins of the active agent accounts, sorted."""
        rows = await self._list_agents.execute(ListAgentsQuery(active_only=True))
        return [r["agent"] for r in rows]

    async def account_kind(self, login: str) -> str | None:
        """The kind of the account LOGIN, or None when the account does not exist."""
        return await self._account_kind.execute(GetAccountKindQuery(login=login))

    async def session_new(
        self, agent: str, label: str, expires_days=7, file=None
    ) -> dict:
        return await self._new_session.execute(
            NewSessionCommand(
                agent=agent, label=label, expires_days=expires_days, file=file
            )
        )

    async def session_list(self, agent: str | None = None) -> list[dict]:
        return await self._list_sessions.execute(ListSessionsQuery(agent=agent))

    async def session_rotate(self, login: str, file=None) -> dict:
        return await self._rotate_session.execute(
            RotateSessionCommand(login=login, file=file)
        )

    async def session_revoke(self, login: str) -> dict:
        return await self._revoke_session.execute(RevokeSessionCommand(login=login))

    async def authenticate(self, login: str, secret: str) -> dict:
        """Check a login and secret. Returns the principal with no hash, plus "fingerprint"
        and "expires_at_value" (the expiry as a datetime)."""
        try:
            return await self._authenticate.execute(
                AuthenticateCommand(login=login, secret=secret)
            )
        except CredentialError as exc:
            raise ToolError(exc.code, path="credential") from None

    async def authenticate_credential(self, credential: object) -> dict:
        """Check a whole "mai1:<login>:<secret>" string."""
        login, secret = self.parse_credential(credential)
        return await self.authenticate(login, secret)

    async def authenticate_owner(self, credential: object) -> dict | None:
        """The owner principal of a credential, or None for any other credential."""
        try:
            principal = await self.authenticate_credential(credential)
        except ToolError:
            return None
        return principal if principal.get("kind") == "owner" else None

    async def login_fingerprint(self, login: str) -> str | None:
        return await self._check_login.execute(CheckLoginQuery(login=login))

    async def roles(self, agent: str) -> dict[str, str]:
        return await self._get_roles.execute(GetRolesQuery(agent=agent))

    def parse_credential(self, credential: object) -> tuple[str, str]:
        try:
            return parse_credential(credential)
        except CredentialError as exc:
            raise ToolError(exc.code, path="credential") from None

    def read_credential_file(self, path: str) -> str:
        """The credential in a file. A missing file gives LOGIN_FAILED, the same as a bad secret,
        so a probe learns nothing about paths."""
        text = self._files.read(path)
        if text is None:
            raise ToolError("LOGIN_FAILED", path="credential_file")
        return text
