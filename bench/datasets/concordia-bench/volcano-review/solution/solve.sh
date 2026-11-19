#!/bin/bash
# Oracle: require and approve the admission security assessment.
set -euo pipefail
unset CI
cp -R /solution/workspace /tmp/ws
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws review
cd /workspace/repo
concordia assessment validate
concordia index
# The first call refuses and prints the subject digest it wants confirmed.
digest="$(concordia assessment approve ASM-ADMISSION --reviewer secops@bench.invalid 2>&1 || true)"
digest="$(printf '%s\n' "$digest" | grep -o 'sha256:[0-9a-f]\{64\}' | tail -1)"
concordia assessment approve ASM-ADMISSION --reviewer secops@bench.invalid --confirm-subject "$digest"
git add -A
git add -f assessments
git commit -q -m "chore: approve ASM-ADMISSION"
concordia index
concordia check --no-color
