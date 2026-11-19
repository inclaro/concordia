# Redaction reference

Concordia keeps secret values out of the text that it shows or stores. This page lists the rules, the marker, the records, and the limits. For how to share a bundle safely, see [Run Concordia in CI](../how-to/run-in-ci.md#share-bundles-safely).

## Where redaction applies

Redaction runs before Concordia prints or stores text that came from repository files or from the provider:

- `explain --source` (pretty and JSON): the snippets of infrastructure files.
- `export bundle --include-source-snippets violations`: the stored snippets.
- `export bundle --include-graph`: the property values of the evidence edges in `graph/evidence.json`. The property names stay. The value of a property with a sensitive name (`api_token`) becomes `[REDACTED:secret-data]`; in any other value, token shapes, private keys, and URL passwords are replaced.
- `export bundle` assurance evidence (`assurance/render/<environment>.json`, `assurance/plans/<environment>.json`, `assurance/statements/<digest>.json`): the rendered manifests, the Terraform or OpenTofu plans, and the in-toto statements. See [rendered Secrets, plan values, and statements](#assurance-evidence).
- `export bundle` review files (`assessments/`, `approvals/`, `verification/`) and the titles of decisions in ADR governance results: the same passes run on each file and title.
- Diagnostics: the message and the hint of every diagnostic pass through the pattern pass (token shapes, PEM blocks, bearer credentials, URL passwords), so a diagnostic that quotes a line or a provider error shows the marker.
- Log lines at every level, including trace lines and diagnostics: the same pattern pass runs on each line before it is written.

A secret value is a credential, token, private key, Secret data, or the value of a sensitive environment variable. Concordia keeps the identifiers: the name of a Secret, the key that refers to it (`secretKeyRef`), and the name of a variable stay in the text.

## Marker

A removed value is replaced by `[REDACTED:<class>]`. The marker has no hash, prefix, or length of the value. A value that spans several lines (a PEM block, a YAML block scalar, a heredoc, a multi-line list) becomes one marker on its first line, so the number of lines of the value is not visible either. The numbers of the lines that follow in the original file stay in the snippet: the numbers of a snippet can skip.

| Class | What it replaces |
|---|---|
| `secret-data` | The value of a sensitive key: Kubernetes Secret `data` and `stringData`, a password in a Terraform variable file or a values file, a sensitive property of the evidence. |
| `env-value` | The value of an environment variable with a sensitive name (`DB_PASSWORD`, `API_TOKEN`) in Kubernetes, Compose, Dockerfile, and shell text. |
| `token` | A value of a known token shape, and the credential of an `Authorization` header. |
| `private-key` | A PEM private key block (`-----BEGIN ... PRIVATE KEY-----` to `-----END ... PRIVATE KEY-----`). |
| `credential` | The password in a URL, such as `postgres://user:<password>@host/db`. |
| `outside-contract` | Not a secret class. A field or facet that an output does not keep because its contract does not list it: a field of a persisted statement outside the contract of its predicate type, or a dependency facet of a review item outside the digest contract. The value is dropped and no marker replaces it. |

## Assurance evidence

Assurance inputs get their own coverage. Each of the three produces records like any other output.

| Input | What is removed | Record |
|---|---|---|
| Rendered manifests (`assurance.rendering`) | Redaction runs on the text of each rendered source before the typed reader parses it, so a Kubernetes `Secret` keeps its name and keys but never its `data` and `stringData` values, and the structure-aware and pattern passes of [Passes](#passes) apply to every document. | `output` `assurance/render/<environment>.json`, `file` of the source. |
| Plans (`assurance.plans`) | Every value that the plan marks `after_sensitive` becomes `[REDACTED:secret-data]` (class `secret-data`). A string attribute that the plan does not mark gets the pattern pass and the sensitive-name rule, so a credential in a plain attribute is removed too. Values that are `after_unknown` have no value and are listed as unknown paths. | `output` `assurance/plans/<environment>.json`, `property` `<resource address>.<attribute path>`. |
| Statements (in-toto v1) | A persisted statement keeps only the fields that the contract of its predicate type declares; strings that remain pass the redaction rules. A field outside the contract is removed and recorded with class `outside-contract` at its path. A predicate type without a contract keeps no predicate, and one record with `property` `predicate` and class `outside-contract` is written. | `output` `assurance/statements/<digest>.json`, `property` the path of the field. |

The digest contract of review items (`dependency` facets outside the contract) records removed values with the class `outside-contract` in the `redactions` of the item. This class names a facet that is not part of the digest contract; it is not a secret class and its record never holds a value.

## Rule set version

The rule set has a version, which is `1` for this release. The bundle manifest records it with the redaction records. A change in what a rule removes changes the version. The same input and the same rule set version give byte-identical output and identical records.

## Passes

A file gets the passes for its type and then the pattern pass.

| Files | Pass | Rules |
|---|---|---|
| `*.yaml`, `*.yml`, `*.json` | Structure-aware, with the YAML parser | In a `kind: Secret` document: every value under `data` and `stringData`. In every document: the annotation `kubectl.kubernetes.io/last-applied-configuration`; the `value` of a list item whose `name` is sensitive (Kubernetes `env`); the values of `environment`, `env`, `args`, `build_args`, and `variables` whose key is sensitive, as a mapping or as `KEY=value` list items (Compose); any other scalar under a sensitive key. A document that does not parse gets a line pass on `key: value` text. A multi-document file is read one document at a time. |
| `Dockerfile`, `Dockerfile.*`, `*.dockerfile`, `Containerfile` | Line pass | `ENV` and `ARG` values with a sensitive name (`KEY=value` and `KEY value`, also across `\` line continuations); sensitive assignments and flags in the other instructions. |
| `*.tfvars` | Line pass | The value of a sensitive variable: a string, a list or object (also over several lines), or a heredoc. Sensitive keys inside an inline object are found too. |
| `*.sh`, `*.bash`, `*.zsh`, `*.ksh`, `.env`, `.env.*`, `*.env`, `.envrc`, and files that start with a shell `#!` line | Line pass | `KEY=value` with a sensitive name (also after `export`, `docker run -e`, or `--password=`), and flags such as `--password <value>`. |
| All files | Pattern pass | PEM private key blocks; GitHub, GitLab, AWS, Slack, Stripe, Google API, npm, SendGrid, and `sk-` tokens; JSON Web Tokens; the value of `Authorization: Bearer`, `Basic`, or `Token`; the password of a URL. |

A name is sensitive when one of its words (the name is split at `_`, `-`, `.`, and camelCase) is `password`, `passwd`, `passphrase`, `pwd`, `secret`, `token`, `credential`, or `bearer`, or two words together are `api key`, `private key`, `access key`, `secret key`, `signing key`, `encryption key`, or similar. A word that only ends with one of them, such as `PGPASSWORD` or `AUTHTOKEN`, counts. A name that ends in a word that points at a secret (`name`, `ref`, `file`, `path`, `url`, `namespace`, `type`, `version`) is not sensitive: `secretName`, `PASSWORD_FILE`, and `tokenUrl` stay. `PWD` and `OLDPWD` stay.

Concordia does not redact a value that is empty, `null`, a boolean, a marker, or a reference to a value somewhere else: `${DB_PASSWORD}`, `$DB_PASSWORD`, `$(vault read ...)`, and `{{ .Values.password }}`.

## Records

Each marker has a record. A record names the place and the class, never the value:

| Field | Meaning |
|---|---|
| `output` | The output that holds the marker, for example `snippets/7c4d....txt`. The caller fills it. |
| `file` | The path of the file. Empty for a property value. |
| `property` | The key or variable name whose value was removed, when the pass knows it. |
| `class` | One of the classes above. `outside-contract` appears in the records of a persisted statement and of a review item (see [Assurance evidence](#assurance-evidence)). |
| `line` | The 1-based line in the original file where the value starts. Not set for a property value. |

The bundle manifest lists the records of the bundle in `redactions`, sorted, and the rule set version in `redactionRules`. An entry has `path` (the bundle file that holds the marker), `file` and `line` (the source file and the line of the value, for a snippet), `property` (the key, when known), `class`, and `count` (the number of equal records). An entry never has the value. A bundle without a redaction has `"redactions": []`.

Each output with at least one record shows the total as the diagnostic `CO-REP-005` (info) on standard error, at every verbosity: `redaction applied: <count> value(s) removed from <output>`. `explain --source` names the output `explain`; `export bundle` names each `snippets/<fingerprint>.txt` and `graph/evidence.json` that has records. Only values that start in the shown window of a snippet are counted.

## Limits

- Redaction removes what the rules know. A secret in a non-infrastructure file with a name that is not sensitive and a shape that is not known stays. Use a sensitive name, or keep the secret out of the repository.
- A snippet that starts inside a multi-line value (for example in the body of a PEM block) is redacted from the whole file, not from the lines alone: the lines of the value are removed from the snippet, and the marker is on the first line of the value, which can be outside the snippet window.
- A base64 value outside a Kubernetes Secret is not decoded and not recognized by its content.
- Redaction does not change the working tree. It changes only the text that Concordia shows or stores.
- Line endings (`\n` or `\r\n`) of a file stay.

## Confinement

Concordia reads source files for snippets, the `inputs/<path>` entries of a bundle, and evidence locations only inside the workspace root and the declared project roots. A path from provider output or repository content that leaves the root is refused: an absolute path, a `..` element, or a symbolic link whose target is outside the root. A refused path gives no content and the diagnostic `CO-REP-006` (error, exit code 2 when it stops a run) that names the path. The command continues with the other paths. A missing file is not a refusal: it gives no snippet.

## Control sequences

Text from repository content, provider output, and node names can hold terminal control sequences. Concordia replaces ANSI escape characters, other control characters, DEL, C1 controls, invisible and bidirectional formatting characters, and invalid UTF-8 bytes with a visible escape such as `\x1b` or `\u202e` in terminal output, SARIF, JUnit, and `explain` output. A newline in a name or message is shown as `\n` in a single-line output. The JSON outputs keep the text as valid escaped JSON strings.

## Repository content is data

Concordia treats repository documents, diagrams, provider output, and infrastructure files as data. It does not run embedded commands, follow diagram links or URLs, run hooks, or load generator extensions. With `--offline` it makes no network call because of repository content.
