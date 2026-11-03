#!/usr/bin/env python3
"""Opt-in, offline app-server fixture: canned account/quota only, never auth or models."""
import json
import os
import sys

with open(os.environ["ROTA_FAKE_CODEX_QUOTA_FILE"], encoding="utf-8") as source:
    quota = json.load(source)
initialized = False
for line in sys.stdin:
    request = json.loads(line)
    method = request.get("method")
    if path := os.environ.get("ROTA_FAKE_CODEX_QUOTA_LOG"):
        with open(path, "a", encoding="utf-8") as log:
            log.write(json.dumps({"method": method, "home": os.environ.get("CODEX_HOME")}) + "\n")
    if method == "initialize":
        result = {}
    elif method == "initialized":
        initialized = True
        continue
    elif initialized and method == "account/read" and request.get("params", {}).get("refreshToken") is False:
        result = {"account": {"type": "chatgpt"}}
    elif initialized and method == "account/rateLimits/read":
        result = quota
    else:
        sys.exit("fake codex quota: unexpected request")
    print(json.dumps({"id": request["id"], "result": result}), flush=True)
