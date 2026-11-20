# Changelog

## v0.1.1

- Git is optional. Without git, outside a git repository, or in a repository without a commit, Concordia reads each project as a plain directory: every file below it except `.git`, `node_modules`, and `.concordia`, with no revision and no warning. `doctor` lists git as optional. Git runs in the C locale, so a translated git no longer breaks the repository check.
- A missing Structurizr CLI is handled. `render --views` writes the views that need no export, skips each view with exports with a `CO-PRJ-008` warning and keeps its files, then exits 5 with `CO-PRJ-001`. `render --format <export>` exits 5 with a message that says the CLI is not available and how to install it.
- `CO-WRK-013` is retired and reserved: a project without a git revision no longer gets a warning.

## v0.1.0

Initial public release.

See the [release notes](docs/README.md#release-notes).
