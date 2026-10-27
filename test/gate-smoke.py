"""Recognize completed smoke assertion failures, excluding runner failures."""
from pathlib import Path
import re
import sys

output = Path(sys.argv[1]).read_text(errors="replace")
output = re.sub(r"\x1b\[[0-9;]*m", "", output)
assertion = re.search(r"(?m)^  FAIL\s+\S", output)
summary = re.search(r"(?m)^Smoke assertions failed\.$", output)
infra = re.search(
    r"died without a summary|checkout changed during smoke|changed the worktrees|"
    r"leak guard|poison|mktemp|ENOSPC|no space left on device|^\s*error:",
    output, re.I | re.M)
raise SystemExit(0 if assertion and summary and not infra else 1)
