"""A fake docker for the shim tests: it records its arguments and stdin, prints FAKE_OUT, and exits FAKE_RC."""

import json
import os
import sys

data = sys.stdin.read() if os.environ.get("FAKE_READ_STDIN", "1") == "1" else ""
with open(os.environ["FAKE_LOG"], "a", encoding="utf-8") as f:
    f.write(
        json.dumps(
            {
                "argv": sys.argv[1:],
                "stdin": data,
                "credential": os.environ.get("MAI_CREDENTIAL"),
            }
        )
        + "\n"
    )
out = os.environ.get("FAKE_OUT", "")
if out:
    sys.stdout.write(out + "\n")
sys.stderr.write(os.environ.get("FAKE_ERR", ""))
sys.exit(int(os.environ.get("FAKE_RC", "0")))
