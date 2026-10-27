"""Emit one bounded, single-line recovery record for the shared verifier."""
import json
from pathlib import Path
import sys


def tail(path):
    with Path(path).open("rb") as stream:
        stream.seek(0, 2)
        stream.seek(max(0, stream.tell() - 4096))
        return stream.read().decode(errors="replace")


print("ROTA-FLAKY " + json.dumps(dict(check=sys.argv[1], firstExit=1, retryExit=0,
                                    firstOutput=tail(sys.argv[2]), retryOutput=tail(sys.argv[3]),
                                    retrySeconds=float(Path(sys.argv[4]).read_text().strip()))))
