"""Entry point of the Postfix milter that checks the From header.

The controller starts it as the postfix user:
    python -m mai.app_milter --socket /var/lib/mai/spool/queue/private/mai-milter
"""

from __future__ import annotations

import argparse
import asyncio
import logging
import signal
import sys

from mai.platform.milter import FromHeaderPolicy, MilterServer


async def _run(socket_path: str, domain: str) -> None:
    server = MilterServer(socket_path, FromHeaderPolicy(domain))
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
    args = parser.parse_args(argv)
    logging.basicConfig(
        level=logging.INFO,
        stream=sys.stderr,
        format="mai-milter: %(levelname)s %(message)s",
    )
    asyncio.run(_run(args.socket, args.domain))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
