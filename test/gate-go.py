"""Select failed packages and their output from go test -json gate logs."""
import json
from pathlib import Path
import sys


def events(path):
    with Path(path).open(errors="replace") as stream:
        for line in stream:
            try:
                event = json.loads(line)
            except ValueError:
                continue  # go may print build diagnostics outside the JSON stream
            if isinstance(event, dict):
                yield event


if sys.argv[1] == "failed":
    failed, started, finished = set(), set(), set()
    ineligible = False
    for event in events(sys.argv[2]):
        package, action = event.get("Package"), event.get("Action")
        output = event.get("Output", "")
        if "panic: test timed out" in output or event.get("FailedBuild"):
            ineligible = True
        if not package or event.get("Test"):
            continue
        if action == "start":
            started.add(package)
        elif action in ("pass", "fail", "skip"):
            finished.add(package)
            if action == "fail":
                failed.add(package)
    if ineligible or not failed or started - finished:
        raise SystemExit(1)  # timeouts/builds/incomplete output must never turn green
    print("\n".join(sorted(failed)))
elif sys.argv[1] == "output":
    for event in events(sys.argv[2]):
        if event.get("Package") == sys.argv[3]:
            print(event.get("Output", json.dumps(event) + "\n"), end="")
else:
    raise SystemExit("unknown operation")
