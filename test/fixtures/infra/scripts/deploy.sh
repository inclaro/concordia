#!/usr/bin/env bash
set -euo pipefail

export GITHUB_TOKEN=ghp_CONCORDIASEEDshelltoken0000000000000

kubectl apply -f k8s/prod/deployment.yaml
helm upgrade --install api charts/api --namespace payments
