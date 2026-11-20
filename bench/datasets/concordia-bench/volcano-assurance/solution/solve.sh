#!/bin/bash
# Oracle: render the Volcano chart, evaluate RBAC and enable the assurance pack.
set -euo pipefail
unset CI
cp -R /solution/workspace /tmp/ws
# The upstream chart links its CRD directories: Concordia refuses to render a symlink, so the rendered environment uses a copy with the links resolved.
mkdir -p /tmp/ws/bench-env/chart
cp -rL /workspace/repo/installer/helm/chart/volcano /tmp/ws/bench-env/chart/volcano
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws assurance
