"""Select failed packages and their output from go test -json gate logs."""
import json
from pathlib import Path
import sys


def events(path):
    for line in Path(path).read_text(errors="replace").splitlines():
        try:
            event = json.loads(line)
        except ValueError:
            continue  # go may also print build diagnostics outside the JSON stream
        if isinstance(event, dict):
            yield event


if sys.argv[1] == "failed":
    rows = list(events(sys.argv[2]))
    failed = sorted({e["Package"] for e in rows
                     if e.get("Action") == "fail" and e.get("Package") and not e.get("Test")})
    started = {e["Package"] for e in rows if e.get("Action") == "start" and e.get("Package")}
    finished = {e["Package"] for e in rows if e.get("Action") in ("pass", "fail", "skip")
                and e.get("Package") and not e.get("Test")}
    if not failed or started - finished:
        raise SystemExit(1)  # interrupted/incomplete output must never turn green
    print("\n".join(failed))
elif sys.argv[1] == "output":
    for event in events(sys.argv[2]):
        if event.get("Package") == sys.argv[3]:
            print(event.get("Output", json.dumps(event) + "\n"), end="")
else:
    raise SystemExit("unknown operation")
