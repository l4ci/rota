#!/usr/bin/env bash
# Backwards-compat wrapper — delegates to runner.sh.
# Sections live under test/sections/; see test/runner.sh.
exec bash "$(dirname "$0")/runner.sh" "$@"
