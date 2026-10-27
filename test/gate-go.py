"""Select failed packages and their output from go test -json gate logs."""
import json
from pathlib import Path
import re
import sys


# Keep these rejection shapes aligned with internal/worker/flaky.go. An
# assertion in one package must not hide an infrastructure failure alongside it.
infrastructure = re.compile(
    r"signal:|\bkilled\b|\[build failed\]|\[setup failed\]|^panic:|fatal error:|"
    r"test timed out|context deadline exceeded|resource temporarily unavailable|"
    r"cannot allocate memory|no space left on device|permission denied|command not found|"
    r"executable file not found|fork/exec|connection refused|network is unreachable|i/o timeout",
    re.I | re.M)


def events(path):
    with Path(path).open(errors="replace") as stream:
        for line in stream:
            try:
                event = json.loads(line)
            except ValueError:
                # go can print build diagnostics outside the JSON stream.
                yield {"Output": line}
                continue
            if isinstance(event, dict):
                yield event


if sys.argv[1] == "failed":
    failed, started, finished = set(), set(), set()
    ineligible = False
    for event in events(sys.argv[2]):
        package, action = event.get("Package"), event.get("Action")
        output = event.get("Output", "")
        if infrastructure.search(output) or event.get("FailedBuild"):
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
        if event.get("Package") == sys.argv[3] and "Output" in event:
            print(event["Output"], end="")
else:
    raise SystemExit("unknown operation")
