"""Entry point of the Postfix milter of mai.

The milter checks the From header, refuses the copies mailbox as a
recipient, and writes the envelope of each accepted mail to PostgreSQL.

The controller starts it as the postfix user:
    python -m mai.app_milter --socket /var/lib/mai/spool/queue/private/mai-milter
"""

from __future__ import annotations

import argparse
import asyncio
import logging
import signal
import sys

from mai.platform.milter import FromHeaderPolicy, MilterServer, PostgresEnvelopeLog


async def _run(socket_path: str, domain: str, dsn: str | None) -> None:
    envelopes = PostgresEnvelopeLog(dsn) if dsn else None
    server = MilterServer(socket_path, FromHeaderPolicy(domain), envelopes=envelopes)
    await server.start()
    loop = asyncio.get_running_loop()
    stop = asyncio.Event()
    for sig in (signal.SIGTERM, signal.SIGINT):
        loop.add_signal_handler(sig, stop.set)
    serving = asyncio.create_task(server.serve_forever())
    await stop.wait()
    serving.cancel()
    await server.close()


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="mai-milter")
    parser.add_argument("--socket", required=True, help="path of the unix socket")
    parser.add_argument(
        "--domain", default="mai.internal", help="mail domain of the logins"
    )
    parser.add_argument(
        "--dsn",
        default="host=/run/postgresql dbname=mai user=postfix",
        help="PostgreSQL connection of the envelope record",
    )
    parser.add_argument(
        "--no-envelope",
        action="store_true",
        help="keep no envelope record (a test with no database)",
    )
    args = parser.parse_args(argv)
    logging.basicConfig(
        level=logging.INFO,
        stream=sys.stderr,
        format="mai-milter: %(levelname)s %(message)s",
    )
    asyncio.run(_run(args.socket, args.domain, None if args.no_envelope else args.dsn))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
