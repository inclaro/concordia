#!/bin/bash
# Oracle: gate the branch with --changed, record the exception authority and the scoped waiver on main.
set -euo pipefail
unset CI
cd /workspace/repo
git checkout -q main
cat >> concordia.yaml <<'EOF2'
assessments:
  trustAnchor:
    ref: main
  authorities:
  - id: arch-exceptions
    role: exception
    mechanism: protected-review
    reviewers: [board@bench.invalid]
    paths: [exceptions/**]
EOF2
mkdir -p assessments
touch assessments/.gitkeep
concordia lock update
concordia waiver add --rule KN-PLC-003 --subject kn-activator --environment edge \
  --approver board@bench.invalid@arch-exceptions --owner team-knative-serving \
  --reason "Activator pilot on the edge cluster" --expires 2099-12-31 --issue KN-PILOT-1
git add -A
git add -f .concordia.lock
git commit -q -m "chore: exception authority and scoped waiver"
git checkout -q feature/bench-change
git merge -q --no-edit main
concordia index
concordia check --no-color --changed main || [ $? -eq 1 ]
cat > review.json <<'EOF2'
{"verdict": "fail", "violations": [
  {"ruleId": "KN-LYR-001", "path": "pkg/apis/serving/bench_rec.go"},
  {"ruleId": "KN-PLC-003", "path": "bench-env/edge/bench-activator.yaml"}
]}
EOF2
