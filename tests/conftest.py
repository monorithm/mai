"""Fixtures for all tests."""

import os

# ConfigModule.for_root() in mai.app_module reads the environment when
# Python imports that file. pytest loads this file first, thus the test
# settings are in the environment before that import.
os.environ["DATABASE_BACKEND"] = "memory"
os.environ.setdefault("SECRETS_DIR", "/tmp/mai-test-secrets")
