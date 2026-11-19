#!/bin/bash
set -uo pipefail
mkdir -p /logs/verifier
PYTHONPATH=/opt/concordia-bench/python /opt/venv/bin/python -m concordia_bench.verify /tests/spec.yaml 2>&1 | tee /logs/verifier/test-console-output.txt
[ -s /logs/verifier/reward.json ] || echo '{"reward": 0.0}' > /logs/verifier/reward.json
exit 0
