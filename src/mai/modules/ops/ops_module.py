"""The ops module: backup, restore, the daily backup, and the 0.5.0 Maildir import."""

from __future__ import annotations

from bustan import ClassProvider, ConfigService, FactoryProvider, Module

from mai.modules.credentials import CredentialsModule
from mai.platform.clock import ClockModule

from .application.ops_paths import OpsPaths
from .application.ops_service import OpsService
from .domain.ports import AccountDirectory, BackupTool, MaildirSource, MailImporter
from .infra.credential_account_directory import CredentialAccountDirectory
from .infra.doveadm_mail_importer import DoveadmMailImporter
from .infra.fs_maildir_source import FsMaildirSource
from .infra.memory_backup_tool import MemoryBackupTool
from .infra.memory_mail_importer import MemoryMailImporter
from .infra.postgres_backup_tool import PostgresBackupTool
from .jobs.daily_backup_job import DailyBackupHandler


def _postgres(config: ConfigService) -> bool:
    return str(config.get("DATABASE_BACKEND", "memory")) == "postgres"


def make_backup_tool(config: ConfigService) -> BackupTool:
    return PostgresBackupTool() if _postgres(config) else MemoryBackupTool()


def make_importer(config: ConfigService) -> MailImporter:
    return DoveadmMailImporter() if _postgres(config) else MemoryMailImporter()


def make_paths() -> OpsPaths:
    return OpsPaths.from_environment()


@Module(
    imports=[ClockModule, CredentialsModule],
    providers=[
        FactoryProvider(provide=OpsPaths, use_factory=make_paths, inject=()),
        FactoryProvider(
            provide=BackupTool, use_factory=make_backup_tool, inject=(ConfigService,)
        ),
        FactoryProvider(
            provide=MailImporter, use_factory=make_importer, inject=(ConfigService,)
        ),
        ClassProvider(provide=MaildirSource, use_class=FsMaildirSource),
        ClassProvider(provide=AccountDirectory, use_class=CredentialAccountDirectory),
        OpsService,
        DailyBackupHandler,
    ],
    exports=[OpsService],
)
class OpsModule:
    pass
