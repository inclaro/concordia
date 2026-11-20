#!/bin/bash
# Oracle: enable the core pack with tags and overrides.
set -euo pipefail
unset CI
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws core
