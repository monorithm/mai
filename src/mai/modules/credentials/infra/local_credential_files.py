import os
from pathlib import Path

from ..domain.credential_files import CredentialFiles


class LocalCredentialFiles(CredentialFiles):
    """Credential files on the local disk. System secrets go in MAI_SECRETS."""

    def __init__(self, secrets_dir: str | None = None) -> None:
        self._secrets_dir = secrets_dir

    def read(self, path: str) -> str | None:
        try:
            text = Path(path).expanduser().read_text(encoding="utf-8").strip()
        except OSError:
            return None
        return text.splitlines()[0].strip() if text else ""

    def write(self, path: str, credential: str) -> None:
        target = Path(path).expanduser()
        target.parent.mkdir(parents=True, exist_ok=True)
        fd = os.open(str(target), os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as handle:
            handle.write(credential + "\n")
        os.chmod(target, 0o600)

    def system_path(self, login: str) -> str:
        base = self._secrets_dir or os.environ.get("MAI_SECRETS", "/data/secrets")
        return str(Path(base) / f"{login}.cred")
