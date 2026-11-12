from typing import Literal

from pydantic import BaseModel, ConfigDict, Field


class AppSettings(BaseModel):
    """The settings of mai, read from the environment and from .env.

    The application does not start if a value is not valid. Each key starts
    with the name of the module that reads it.
    """

    model_config = ConfigDict(extra="ignore")

    DATABASE_BACKEND: Literal["memory", "postgres"] = "memory"
    DATABASE_DSN: str = Field(
        default="host=/run/postgresql dbname=mai user=mai", min_length=1
    )

    MAIL_DOMAIN: str = Field(default="mai.internal", min_length=1)
    MAIL_IMAP_HOST: str = "127.0.0.1"
    MAIL_IMAP_PORT: int = Field(default=1143, ge=1, le=65535)
    MAIL_SMTP_HOST: str = "127.0.0.1"
    MAIL_SMTP_PORT: int = Field(default=1588, ge=1, le=65535)

    MCP_HOST_ALLOWLIST: str = "127.0.0.1,localhost"

    SECRETS_DIR: str = Field(default="/data/secrets", min_length=1)

    HTTP_HOST: str = "0.0.0.0"
    HTTP_PORT: int = Field(default=8080, ge=1, le=65535)

    MILTER_SOCKET: str = "/data/postfix/private/mai-milter"
