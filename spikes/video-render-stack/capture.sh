#!/bin/bash
T=$(mktemp -d); H=$(mktemp -d); X=$(mktemp -d)
cd $T && git init -q
export HOME=$H XDG_CONFIG_HOME=$X
{ echo "### rota --version"; rota --version 2>&1; echo "### rota init"; rota init 2>&1; echo "### rota --help | head -15"; rota --help 2>&1 | head -15; } > "$1"
echo "$T $H" >> "$1.paths"
