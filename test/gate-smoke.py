"""Recognize completed smoke assertion failures, excluding runner failures."""
from pathlib import Path
import re
import sys

# Share the Go rule's infrastructure markers, then add runner-only failures
# without changing which failures qualify for a Go retry.
root = Path(__file__).resolve().parent.parent
marker_files = (root / "internal/flakeledger/infrastructure-markers.txt",
                root / "test/gate-smoke-markers.txt")
infrastructure = re.compile("|".join(
    marker for path in marker_files for marker in path.read_text().splitlines() if marker),
    re.I | re.M)

output = Path(sys.argv[1]).read_text(errors="replace")
output = re.sub(r"\x1b\[[0-9;]*m", "", output)
assertion = re.search(r"(?m)^  FAIL\s+\S", output)
summary = re.search(r"(?m)^Smoke assertions failed\.$", output)
infra = infrastructure.search(output)
raise SystemExit(0 if assertion and summary and not infra else 1)
