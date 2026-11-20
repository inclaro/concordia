#!/bin/bash
unset CI
# Oracle: apply the assurance workspace over the adopted repository.
set -euo pipefail
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws assurance
