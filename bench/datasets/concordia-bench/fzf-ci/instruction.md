You are a developer on the `fzf` project at `/workspace/repo` (a git repository on branch `main`). Concordia is installed (`concordia --help`); its documentation is in `/opt/concordia/docs`. Concordia reads the committed state of the repository: commit your changes. Do not modify source code of the project unless the task says so.

# Add the Concordia CI pipeline

Concordia is already adopted on `main`, with the packs `fzf-adopt` and `fzf-structure`. Add it to continuous integration.

1. Use Concordia's GitHub Actions template at `.github/workflows/concordia.yml`, changed so that the check step runs Concordia in offline mode.
2. Configure the workspace so that every check also writes a JUnit report to `build/concordia/results.xml` and a SARIF report to `build/concordia/results.sarif`.
3. Run the pipeline locally once (the index and the offline check) to confirm that it passes.
4. Commit on `main`.
