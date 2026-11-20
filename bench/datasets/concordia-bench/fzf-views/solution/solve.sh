#!/bin/bash
# Oracle: declare the views, render the generated one and commit the outputs.
set -euo pipefail
unset CI
cd /workspace/repo
cp -R /solution/workspace/. .
# The rendered outputs of the workspace are for the use task's stage image; render them for real here.
rm -f docs/architecture/fzf-container.*
concordia lock update
concordia model validate
concordia bind validate
git add -A
git add -f .concordia.lock
git commit -q -m "chore: declare the architecture views"
concordia index
concordia render --views
git add -A docs/architecture
git commit -q -m "docs: render the fzf views"
concordia index
concordia check --no-color
