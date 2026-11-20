#!/bin/bash
# Oracle: apply the reference workspace of the assurance scenario.
set -euo pipefail
unset CI
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws assurance
