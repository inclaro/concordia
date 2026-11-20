#!/bin/bash
unset CI
# Oracle: apply the review reference workspace, then approve as the reviewer.
# The workspace also holds the approval record, so that the stage image that the use task
# starts from has it; the oracle makes the approval itself, as an agent does.
set -euo pipefail
cp -R /solution/workspace /tmp/ws
rm -rf /tmp/ws/assessments/approvals
/opt/concordia-bench/bin/apply-workspace.sh /tmp/ws review
cd /workspace/repo
out="$(concordia assessment approve ASM-LLM-EGRESS --reviewer secops@bench.invalid 2>&1 || true)"
digest="$(awk '/^Subject digest:/ {print $3}' <<<"$out")"
concordia assessment approve ASM-LLM-EGRESS --reviewer secops@bench.invalid --confirm-subject "$digest"
git add -f assessments
git commit -q -m "chore: approve ASM-LLM-EGRESS"
