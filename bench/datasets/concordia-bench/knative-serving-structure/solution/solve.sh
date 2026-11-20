#!/bin/bash
# Oracle: add the structure pack from the reference workspace.
set -euo pipefail
unset CI
/opt/concordia-bench/bin/apply-workspace.sh /solution/workspace structure
