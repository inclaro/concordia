#!/bin/bash
# Oracle: add the assurance workspace from the reference workspace.
set -euo pipefail
unset CI
/opt/concordia-bench/bin/apply-workspace.sh /solution/workspace assurance
