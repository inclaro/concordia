#!/bin/bash
# Oracle: add the GitHub Actions template, offline check, JUnit and SARIF outputs.
set -euo pipefail
unset CI
cd /workspace/repo
concordia init --ci github
sed -i 's/run: concordia check/run: concordia --offline check/' .github/workflows/concordia.yml
/opt/venv/bin/python - <<'PY'
import yaml
with open("concordia.yaml") as f:
    cfg = yaml.safe_load(f)
cfg.setdefault("report", {})["outputs"] = [
    {"format": "junit", "path": "build/concordia/results.xml"},
    {"format": "sarif", "path": "build/concordia/results.sarif"},
]
with open("concordia.yaml", "w") as f:
    yaml.safe_dump(cfg, f, sort_keys=False)
PY
git add -A .github concordia.yaml .gitignore .concordia.lock
git commit -q -m "ci: add the Concordia architecture gate"
concordia index
concordia --offline check --no-color
