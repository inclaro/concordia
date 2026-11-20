#!/bin/bash
# Oracle: declare the generated views, render them and commit the outputs.
set -euo pipefail
unset CI
cd /workspace/repo
cp -R /solution/workspace/. .
concordia lock update
concordia model validate
concordia index
concordia render --views
git add -A
git add -f concordia.yaml .concordia.lock architecture bindings docs/architecture
git commit -q -m "chore: declare and render views"
concordia index
concordia check --no-color
