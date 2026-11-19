#!/usr/bin/env sh
set -eu
# Install codebase-memory-mcp 0.12.x first (see https://github.com/DeusData/codebase-memory-mcp).
npx concordia index
npx concordia check --format json --output build/concordia/results.json
