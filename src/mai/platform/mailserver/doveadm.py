"""The argv of a doveadm mail command, sent through the doveadm server of Dovecot.

The app runs as the user mai. A local doveadm mail command needs the
userdb socket of Dovecot and the mail files of the user vmail, and mai
has access to neither. Thus each mail command of the app goes to the doveadm
server socket with "-S <socket>". The doveadm server runs as root, reads the
mail as vmail, and enforces the ACL of the user that "-u" names. The socket
belongs to mai with mode 0600 (see etc/dovecot/dovecot.conf).

The socket accepts only move, "acl set", "acl delete" and "acl get"
(doveadm_allowed_commands). A command that runs as root, for example the
Maildir import from the CLI, uses a local doveadm with no "-S": root has the
access, and the local command has no allowlist. On a host with no socket
(unit tests, a local run), the argv also has no "-S".
"""

from __future__ import annotations

import os

DEFAULT_DOVEADM_SOCKET = "/run/dovecot/doveadm-server"


def doveadm_socket() -> str | None:
    """The doveadm server socket, or None for root or when the socket does not exist."""
    if getattr(os, "geteuid", lambda: -1)() == 0:
        return None
    path = os.environ.get("MAI_DOVEADM_SOCKET", DEFAULT_DOVEADM_SOCKET)
    return path if path and os.path.exists(path) else None


def doveadm_argv(
    command: tuple[str, ...] | list[str],
    args: tuple[str, ...] | list[str],
    exe: str = "doveadm",
    socket: str | None = None,
) -> list[str]:
    """Give [exe, *command, "-S", socket, *args].

    The option "-S" must come after the words of the command, for example
    "doveadm acl set -S <socket> -u ...". If socket is None, the function uses
    doveadm_socket(), and if that is also None, the argv has no "-S".
    """
    path = socket if socket is not None else doveadm_socket()
    argv = [exe, *command]
    if path:
        argv += ["-S", path]
    return argv + list(args)
