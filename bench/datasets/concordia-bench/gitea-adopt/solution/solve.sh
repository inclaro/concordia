#!/bin/bash
# Oracle: adopt Concordia from the reference workspace.
unset CI
set -euo pipefail
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/adopt-workspace.sh /tmp/ws
