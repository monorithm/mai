"""The public names of the credentials module. Other modules import only these."""

from .api.owner_token import OwnerToken
from .application.agent_keys import AgentKeys
from .application.credential_service import CredentialService
from .credentials_module import CredentialsModule
from .domain.credential import (
    ACL_RIGHTS,
    BOARD_ACL_RIGHTS,
    DOMAIN,
    address,
    check_label,
)
from .domain.errors import AccountStateError, CredentialError, UnknownAccountError
from .infra.postgres_agent_key_store import MIGRATIONS as _KEY_MIGRATIONS
from .infra.postgres_credential_store import MIGRATIONS as _ACCOUNT_MIGRATIONS

MIGRATIONS = [*_ACCOUNT_MIGRATIONS, *_KEY_MIGRATIONS]

__all__ = [
    "ACL_RIGHTS",
    "BOARD_ACL_RIGHTS",
    "DOMAIN",
    "MIGRATIONS",
    "AccountStateError",
    "AgentKeys",
    "CredentialError",
    "CredentialService",
    "CredentialsModule",
    "OwnerToken",
    "UnknownAccountError",
    "address",
    "check_label",
]
