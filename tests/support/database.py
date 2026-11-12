"""The database backends of the port contract tests.

The PostgreSQL backend needs the container, thus it is in BACKENDS only when
MAI_IT is set.
"""

import os

BACKENDS = ["memory", "postgres"] if os.environ.get("MAI_IT") else ["memory"]
