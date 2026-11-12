"""The HTTP entry point: the application factory and the command mai serve-http."""

import argparse
import asyncio
import os

from bustan import Application, create_app

from .platform.contract import BACKGROUND_ENV

DEFAULT_HOST = "0.0.0.0"
DEFAULT_PORT = 8080


def create_asgi_app() -> Application:
    """Return the application. It is the ASGI callable that uvicorn runs."""
    from .app_module import AppModule

    return create_app(AppModule)


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
