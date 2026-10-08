#!/usr/bin/env python3
"""Scripted Codex skill worker: installation/output contract, not model inference.

Only two fixture prompts are supported. Read installed skills and use the real
rota file backend in the caller's sandbox; never call a model, host or forge.
The preview layout comes from the installed reference, not a second copy here.
"""
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import textwrap


def rota(*args):
    result = subprocess.run(
        [os.environ["ROTA_BIN"], "--json", *args],
        check=True, capture_output=True, text=True,
    )
    return json.loads(result.stdout)["data"]


def read_skill(name):
    root = Path.cwd() / ".agents" / "skills" / name
    body = (root / "SKILL.md").read_text()
    if not re.search(r"^name: " + re.escape(name) + r"$", body, re.M):
        raise ValueError("installed skill has the wrong name")
    return root


def run(prompt):
    preview = re.fullmatch(r"[/$]rota-work --preview ([BFT][0-9]+)", prompt)
    capture = re.fullmatch(r"[/$]rota-capture task: (\S[^\n]*)", prompt)
    if preview:
        root = read_skill("rota-work")
        target = preview[1]
        title = rota("item", "field", "get", target, "--name", "title")["value"]
        reference = (root / "references" / "work-preview.md").read_text()
        template = re.search(r"```\n(\s*Peek for <target>:.*?)\n\s*```", reference, re.S)
        if not template:
            raise ValueError("installed preview reference has no output template")
        # This fixture is single-repo with no decisions. Omit optional sections.
        peek = textwrap.dedent(template[1])
        peek = re.sub(r"\nRepo\n.*?(?=\nFiles I'd touch)", "\n", peek, flags=re.S)
        peek = re.sub(r"\nHard boundaries to respect\n.*?(?=\nTests I'd add)", "\n", peek, flags=re.S)
        peek = peek.replace("<target>", target)
        peek = re.sub(r"<[^>]+>", lambda _: title, peek)
        return peek
    if capture:
        read_skill("rota-capture")
        title = capture[1]
        data = rota("item", "create", "--kind", "tasks", "--title", title,
                    "--desc", "Scripted Codex capture fixture.")
        return f"- [{data['id']}] {title}"
    raise ValueError("unsupported scripted skill prompt")


try:
    if len(sys.argv) != 2:
        raise ValueError("expected one prompt")
    message = run(sys.argv[1])
    print(json.dumps({"type": "item.completed", "item": {"type": "agent_message", "text": message}}))
    print(json.dumps({"type": "turn.completed"}))
except (ValueError, OSError, KeyError, subprocess.CalledProcessError) as error:
    print(f"fake codex skill worker: {error}", file=sys.stderr)
    sys.exit(98)
