"""The HTTP entry point: the application factory and the command mai serve-http."""

import argparse
import asyncio
import os

from bustan import Application, RequestLimits, create_app

from .platform.contract import BACKGROUND_ENV

DEFAULT_HOST = "0.0.0.0"
DEFAULT_PORT = 8080
# The longest wait of wait_for_mail (MAX_WAIT, 50 s) and a margin. The default
# request limit of bustan (30 s) stops a longer wait with HTTP 504 and no code.
# The time limits of the lease operations are less than this limit.
REQUEST_TIMEOUT_SECONDS = 75.0
REQUEST_LIMITS = RequestLimits(timeout_seconds=REQUEST_TIMEOUT_SECONDS)


def create_asgi_app() -> Application:
    """Return the application. It is the ASGI callable that uvicorn runs."""
    from .app_module import AppModule

    return create_app(AppModule, request_limits=REQUEST_LIMITS)


def serve(host: str = DEFAULT_HOST, port: int = DEFAULT_PORT) -> None:
    """Serve MCP, the UI and /healthz on HTTP with uvicorn, until a signal.

    This process is the only one that runs the router and the scheduled jobs.
    """
    os.environ.setdefault(BACKGROUND_ENV, "on")
    asyncio.run(create_asgi_app().listen(port=port, host=host))


def main(argv: list[str] | None = None) -> int:
    parser = argparse.ArgumentParser(prog="mai-serve", description="Serve mai on HTTP.")
    parser.add_argument("--host", default=os.environ.get("HTTP_HOST", DEFAULT_HOST))
    parser.add_argument(
        "--port", type=int, default=int(os.environ.get("HTTP_PORT", DEFAULT_PORT))
    )
    args = parser.parse_args(argv)
    serve(args.host, args.port)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
