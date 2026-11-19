package diag

// Diagnostic code constants. Rule IDs (ruleId) are a different namespace.
const (
	WRK001 = "CO-WRK-001"
	WRK002 = "CO-WRK-002"
	WRK003 = "CO-WRK-003"
	WRK004 = "CO-WRK-004"
	WRK005 = "CO-WRK-005"
	WRK010 = "CO-WRK-010"
	WRK011 = "CO-WRK-011"
	WRK012 = "CO-WRK-012"
	WRK013 = "CO-WRK-013"
	WRK014 = "CO-WRK-014"
	WRK015 = "CO-WRK-015"
	WRK016 = "CO-WRK-016"
	WRK017 = "CO-WRK-017"
	WRK018 = "CO-WRK-018"
	WRK099 = "CO-WRK-099"
	INT001 = "CO-INT-001"
	INT002 = "CO-INT-002"
	INT003 = "CO-INT-003"
	INT004 = "CO-INT-004"
	INT005 = "CO-INT-005"
	INT006 = "CO-INT-006"
	INT007 = "CO-INT-007"
	INT008 = "CO-INT-008"
	INT009 = "CO-INT-009"
	INT010 = "CO-INT-010"
	INT012 = "CO-INT-012"
	INT013 = "CO-INT-013"
	INT014 = "CO-INT-014"
	INT015 = "CO-INT-015"
	OBS001 = "CO-OBS-001"
	OBS002 = "CO-OBS-002"
	OBS003 = "CO-OBS-003"
	OBS004 = "CO-OBS-004"
	OBS005 = "CO-OBS-005"
	OBS006 = "CO-OBS-006"
	OBS008 = "CO-OBS-008"
	OBS010 = "CO-OBS-010"
	OBS011 = "CO-OBS-011"
	OBS012 = "CO-OBS-012"
	OBS013 = "CO-OBS-013"
	OBS014 = "CO-OBS-014"
	OBS015 = "CO-OBS-015"
	OBS016 = "CO-OBS-016"
	OBS017 = "CO-OBS-017"
	OBS018 = "CO-OBS-018"
	OBS019 = "CO-OBS-019"
	OBS020 = "CO-OBS-020"
	OBS021 = "CO-OBS-021"
	BND001 = "CO-BND-001"
	BND002 = "CO-BND-002"
	BND003 = "CO-BND-003"
	BND004 = "CO-BND-004"
	BND005 = "CO-BND-005"
	BND006 = "CO-BND-006"
	BND008 = "CO-BND-008"
	BND009 = "CO-BND-009"
	POL001 = "CO-POL-001"
	POL002 = "CO-POL-002"
	POL003 = "CO-POL-003"
	POL004 = "CO-POL-004"
	POL005 = "CO-POL-005"
	POL009 = "CO-POL-009"
	POL010 = "CO-POL-010"
	POL011 = "CO-POL-011"
	POL012 = "CO-POL-012"
	POL013 = "CO-POL-013"
	POL014 = "CO-POL-014"
	POL015 = "CO-POL-015"
	POL016 = "CO-POL-016"
	POL017 = "CO-POL-017"
	POL018 = "CO-POL-018"
	CON021 = "CO-CON-021"
	CON022 = "CO-CON-022"
	CON023 = "CO-CON-023"
	CON024 = "CO-CON-024"
	CON025 = "CO-CON-025"
	CON026 = "CO-CON-026"
	CON027 = "CO-CON-027"
	CON028 = "CO-CON-028"
	CON029 = "CO-CON-029"
	CON030 = "CO-CON-030"
	CON031 = "CO-CON-031"
	CON032 = "CO-CON-032"
	CON033 = "CO-CON-033"
	CON099 = "CO-CON-099"
	EXC001 = "CO-EXC-001"
	EXC002 = "CO-EXC-002"
	EXC003 = "CO-EXC-003"
	EXC004 = "CO-EXC-004"
	EXC006 = "CO-EXC-006"
	EXC007 = "CO-EXC-007"
	EXC008 = "CO-EXC-008"
	EXC009 = "CO-EXC-009"
	EXC010 = "CO-EXC-010"
	EXC011 = "CO-EXC-011"
	PRJ001 = "CO-PRJ-001"
	PRJ002 = "CO-PRJ-002"
	PRJ005 = "CO-PRJ-005"
	PRJ006 = "CO-PRJ-006"
	PRJ007 = "CO-PRJ-007"
	PRJ008 = "CO-PRJ-008"
	DGM001 = "CO-DGM-001"
	DGM002 = "CO-DGM-002"
	DGM003 = "CO-DGM-003"
	DGM004 = "CO-DGM-004"
	DGM005 = "CO-DGM-005"
	ADR001 = "CO-ADR-001"
	ADR002 = "CO-ADR-002"
	ADR003 = "CO-ADR-003"
	ADR004 = "CO-ADR-004"
	ADR005 = "CO-ADR-005"
	ADR006 = "CO-ADR-006"
	ADR007 = "CO-ADR-007"
	ADR008 = "CO-ADR-008"
	ADR009 = "CO-ADR-009"
	ASM001 = "CO-ASM-001"
	ASM002 = "CO-ASM-002"
	ASM003 = "CO-ASM-003"
	ASM004 = "CO-ASM-004"
	ASM005 = "CO-ASM-005"
	ASM006 = "CO-ASM-006"
	ASM007 = "CO-ASM-007"
	ASM008 = "CO-ASM-008"
	ASM009 = "CO-ASM-009"
	ASM010 = "CO-ASM-010"
	ASM011 = "CO-ASM-011"
	SEC001 = "CO-SEC-001"
	SEC002 = "CO-SEC-002"
	REP001 = "CO-REP-001"
	REP002 = "CO-REP-002"
	REP003 = "CO-REP-003"
	REP004 = "CO-REP-004"
	REP005 = "CO-REP-005"
	REP006 = "CO-REP-006"
	PRV001 = "CO-PRV-001"
	PRV002 = "CO-PRV-002"
	PRV003 = "CO-PRV-003"
	PRV004 = "CO-PRV-004"
	PRV005 = "CO-PRV-005"
	PRV006 = "CO-PRV-006"
	PRV007 = "CO-PRV-007"
	PRV008 = "CO-PRV-008"
	PRV009 = "CO-PRV-009"
	PRV010 = "CO-PRV-010"
	PRV011 = "CO-PRV-011"
	PRV012 = "CO-PRV-012"
	PRV013 = "CO-PRV-013"
	PRV014 = "CO-PRV-014"
	QUA001 = "CO-QUA-001"
	QUA002 = "CO-QUA-002"
	QUA003 = "CO-QUA-003"
)

// NoExit is the exit code of a catalog entry that never stops a run by itself.
const NoExit = -1

// Entry is one catalog entry.
type Entry struct {
	// Code is the diagnostic code, for example "CO-WRK-001".
	Code string
	// Severity is the default severity of a diagnostic with this code.
	Severity Severity
	// Message is the template. "{name}" is replaced by the parameter "name".
	Message string
	// Suggestion is the optional hint template.
	Suggestion string
	// ExitCode is the default exit code when this code stops the run, or NoExit.
	ExitCode int
}

// entries is the catalog of all diagnostic codes. All diagnostics MUST be made
// from this catalog (New, Fail).
var entries = []Entry{
	{Code: WRK001, Severity: SeverityError, ExitCode: InvalidInput, Message: "workspace file not found: {path}", Suggestion: "run `concordia init` or pass --workspace <path>"},
	{Code: WRK002, Severity: SeverityError, ExitCode: InvalidInput, Message: "workspace not valid: {detail}"},
	{Code: WRK003, Severity: SeverityWarning, ExitCode: NoExit, Message: "lockfile does not agree with the found toolchain: {detail}", Suggestion: "review the toolchain change, then update .concordia.lock"},
	{Code: WRK004, Severity: SeverityError, ExitCode: InvalidInput, Message: "profile not valid: unknown profile '{profile}'"},
	{Code: WRK005, Severity: SeverityWarning, ExitCode: NoExit, Message: "existing file not overwritten: {path}", Suggestion: "use --force to overwrite"},
	{Code: WRK010, Severity: SeverityError, ExitCode: InvalidInput, Message: "usage error: {detail}"},
	{Code: WRK011, Severity: SeverityError, ExitCode: InvalidInput, Message: "destructive action needs confirmation: {detail}", Suggestion: "pass --yes to confirm"},
	{Code: WRK012, Severity: SeverityError, ExitCode: Toolchain, Message: "git not available or git command failed: {detail}", Suggestion: "install git, and make sure that `git status` works in the workspace"},
	{Code: WRK013, Severity: SeverityWarning, ExitCode: NoExit, Message: "no git revision for project {project}: the project root is not in a git repository or has no commit", Suggestion: "run Concordia in a git repository with at least one commit"},
	{Code: WRK014, Severity: SeverityError, ExitCode: Toolchain, Message: "cosign not available: {detail}", Suggestion: "install cosign, or set COSIGN to the path of cosign"},
	{Code: WRK015, Severity: SeverityError, ExitCode: InvalidInput, Message: "git reference not valid: {detail}"},
	{Code: WRK016, Severity: SeverityError, ExitCode: Network, Message: "cosign needs network access, and --offline is set: {detail}", Suggestion: "remove --offline; to verify a signature offline, give --trusted-root with a Sigstore trusted root file"},
	{Code: WRK017, Severity: SeverityError, ExitCode: InvalidInput, Message: "environment not declared: {detail}", Suggestion: "declare the environment under `environments` in the workspace file, or fix the --environment value"},
	{Code: WRK018, Severity: SeverityError, ExitCode: InvalidInput, Message: "check.rollback expired on {expires}: {detail}", Suggestion: "remove `check.rollback` to enforce again, or set a later `expires` date with a new reason"},
	{Code: WRK099, Severity: SeverityError, ExitCode: Internal, Message: "internal error: {detail}"},
	{Code: INT001, Severity: SeverityError, ExitCode: InvalidInput, Message: "CALM document cannot be read: {detail}"},
	{Code: INT002, Severity: SeverityError, ExitCode: InvalidInput, Message: "CALM schema violation: {detail}"},
	{Code: INT003, Severity: SeverityError, ExitCode: InvalidInput, Message: "CALM schema version not supported: {schema}", Suggestion: "Concordia supports CALM schema releases 1.2 and 1.3"},
	{Code: INT004, Severity: SeverityError, ExitCode: InvalidInput, Message: "duplicate unique-id: {id}"},
	{Code: INT005, Severity: SeverityError, ExitCode: InvalidInput, Message: "relationship endpoint not found: {id}"},
	{Code: INT006, Severity: SeverityError, ExitCode: InvalidInput, Message: "pattern violation: {detail}"},
	{Code: INT007, Severity: SeverityError, ExitCode: InvalidInput, Message: "Concordia extension not valid: {detail}"},
	{Code: INT008, Severity: SeverityError, ExitCode: InvalidInput, Message: "CALM decorator not valid: {detail}"},
	{Code: INT009, Severity: SeverityError, ExitCode: InvalidInput, Message: "decorator applies-to refers to an unknown unique-id: {id}"},
	{Code: INT010, Severity: SeverityWarning, ExitCode: NoExit, Message: "decorator does not target the architecture: {path}", Suggestion: "add the architecture to target, or remove the decorator from architecture.decorators"},
	{Code: INT012, Severity: SeverityError, ExitCode: Network, Message: "remote reference not resolved or not locked: {detail}", Suggestion: "map the URL in architecture.urlMapping and run once without --offline to lock it"},
	{Code: INT013, Severity: SeverityError, ExitCode: Network, Message: "remote resource not fetched: {detail}", Suggestion: "check the network, or fill the cache with `concordia lock update`"},
	{Code: INT014, Severity: SeverityError, ExitCode: InvalidInput, Message: "CALM rule violation: {detail}"},
	{Code: INT015, Severity: SeverityWarning, ExitCode: NoExit, Message: "CALM rule warning: {detail}"},
	{Code: OBS001, Severity: SeverityError, ExitCode: AdapterFailure, Message: "CBM call failed: {detail}"},
	{Code: OBS002, Severity: SeverityError, ExitCode: AdapterFailure, Message: "project not indexed, not ready, or index timeout: {detail}", Suggestion: "run `concordia index`"},
	{Code: OBS003, Severity: SeverityError, ExitCode: Toolchain, Message: "CBM executable missing: {executable}", Suggestion: "install codebase-memory-mcp or set observation.executable"},
	{Code: OBS004, Severity: SeverityError, ExitCode: Toolchain, Message: "CBM version not compatible: found {version}, supported 0.12.x"},
	{Code: OBS005, Severity: SeverityError, ExitCode: Toolchain, Message: "another CBM build is installed: {path} has {found}, Concordia is tested with {version} ({pinned})", Suggestion: "use one CBM binary for your agents and Concordia; see docs/explanation/cbm-and-agents.md"},
	{Code: OBS006, Severity: SeverityError, ExitCode: Network, Message: "pinned CBM build not installed: {detail}", Suggestion: "check the network and try again, or set CONCORDIA_CBM_DOWNLOAD_URL to a mirror"},
	{Code: OBS008, Severity: SeverityError, ExitCode: AdapterFailure, Message: "graph generation stale or not stable: {detail}", Suggestion: "run `concordia index`"},
	{Code: OBS010, Severity: SeverityError, ExitCode: AdapterFailure, Message: "query row count does not agree: {detail}"},
	{Code: OBS011, Severity: SeverityError, ExitCode: AdapterFailure, Message: "path cannot be put in a query: {path}"},
	{Code: OBS012, Severity: SeverityError, ExitCode: InvalidInput, Message: "runtime trace file not valid: {detail}"},
	{Code: OBS013, Severity: SeverityWarning, ExitCode: NoExit, Message: "runtime spans skipped: {detail}"},
	{Code: OBS014, Severity: SeverityWarning, ExitCode: NoExit, Message: "cross-project matching not done for {project}: {detail}"},
	{Code: OBS015, Severity: SeverityWarning, ExitCode: NoExit, Message: "cross-project edges without a resolved target: {count}"},
	{Code: OBS016, Severity: SeverityWarning, ExitCode: NoExit, Message: "native edge kind kept as unsupported evidence: {kind} ({count} edges)"},
	{Code: OBS017, Severity: SeverityWarning, ExitCode: NoExit, Message: "capability profile {profile}: {detail}"},
	{Code: OBS018, Severity: SeverityWarning, ExitCode: NoExit, Message: "scope failure in project {project}: {detail}; its coverage is failed and the run continues"},
	{Code: OBS019, Severity: SeverityWarning, ExitCode: NoExit, Message: "native edges kept as ambiguous evidence: {kind} ({count} edges): {detail}"},
	{Code: OBS020, Severity: SeverityWarning, ExitCode: NoExit, Message: "environment {environment} source {source} is under the provider default-excluded directory {directory}: {hint}"},
	{Code: OBS021, Severity: SeverityWarning, ExitCode: NoExit, Message: "competing declarations of {id}: {locations}: {detail}"},
	{Code: BND001, Severity: SeverityError, ExitCode: InvalidInput, Message: "binding refers to an unknown CALM ID: {id}"},
	{Code: BND002, Severity: SeverityError, ExitCode: InvalidInput, Message: "binding selector not valid: {detail}"},
	{Code: BND003, Severity: SeverityError, ExitCode: InvalidInput, Message: "ambiguous exclusive binding: {detail}"},
	{Code: BND004, Severity: SeverityError, ExitCode: InvalidInput, Message: "required binding matched zero entities: {id}"},
	{Code: BND005, Severity: SeverityWarning, ExitCode: NoExit, Message: "observed entity has no binding: {path}"},
	{Code: BND006, Severity: SeverityError, ExitCode: InvalidInput, Message: "duplicate binding ID: {id}"},
	{Code: BND008, Severity: SeverityError, ExitCode: InvalidInput, Message: "binding with cardinality one matched more than one entity: {detail}"},
	{Code: BND009, Severity: SeverityWarning, ExitCode: NoExit, Message: "ambiguous non-exclusive bindings: {detail}"},
	{Code: POL001, Severity: SeverityError, ExitCode: InvalidInput, Message: "policy not valid: {detail}"},
	{Code: POL002, Severity: SeverityError, ExitCode: InvalidInput, Message: "selector, predicate, or traversal not supported: {detail}"},
	{Code: POL003, Severity: SeverityError, ExitCode: InvalidInput, Message: "duplicate rule ID: {id}"},
	{Code: POL004, Severity: SeverityError, ExitCode: InvalidInput, Message: "unknown edge kind: {kind}"},
	{Code: POL005, Severity: SeverityError, ExitCode: InvalidInput, Message: "unknown CALM ID in a selector or control: {id}"},
	{Code: POL009, Severity: SeverityWarning, ExitCode: NoExit, Message: "evidence kind not supported by the observation provider: {kind} (rule {ruleId})"},
	{Code: POL010, Severity: SeverityError, ExitCode: InvalidInput, Message: "policy pack digest does not agree with its pin: {detail}", Suggestion: "review the change; if you trust it, run `concordia policy add <file> --force` to pin the new digest"},
	{Code: POL011, Severity: SeverityError, ExitCode: InvalidInput, Message: "policy pack file not found or not readable: {path}", Suggestion: "vendor the pack with `concordia policy add <source>`"},
	{Code: POL012, Severity: SeverityError, ExitCode: InvalidInput, Message: "unknown built-in policy pack: {name}", Suggestion: "run `concordia policy list` to see the built-in packs"},
	{Code: POL013, Severity: SeverityError, ExitCode: InvalidInput, Message: "policy pack pin not valid: {detail}"},
	{Code: POL014, Severity: SeverityError, ExitCode: InvalidInput, Message: "policy pack source must be a local file or a built-in pack: {source}", Suggestion: "vendor the pack with `concordia policy add <url>`, then use the vendored file"},
	{Code: POL015, Severity: SeverityError, ExitCode: InvalidInput, Message: "rule override names a rule that the pack does not have: {id} (pack {pack})"},
	{Code: POL016, Severity: SeverityError, ExitCode: Network, Message: "policy pack not fetched: {detail}", Suggestion: "check the URL and the network, or download the file and run `concordia policy add <file>`"},
	{Code: POL017, Severity: SeverityError, ExitCode: InvalidInput, Message: "policy conflict: {detail}", Suggestion: "list both rules in a pack `precedence` (a versioned list of rule IDs), or change one of the rules; no rule has implicit priority"},
	{Code: POL018, Severity: SeverityError, ExitCode: InvalidInput, Message: "enforcement not valid: rule {rule} is mandatory in {environment}, but a guarantee that it depends on is absent: {guarantee}", Suggestion: "provide the guarantee, or set the rule to `advisory` or `shadow` in `check.enforcement`; Concordia never lowers a mode itself"},
	{Code: CON021, Severity: SeverityWarning, ExitCode: NoExit, Message: "minimum coverage not met: {ratio} < {required}"},
	{Code: CON022, Severity: SeverityInfo, ExitCode: NoExit, Message: "rule selects no subject"},
	{Code: CON023, Severity: SeverityWarning, ExitCode: NoExit, Message: "evidence below the confidence threshold: {count} edge(s)"},
	{Code: CON024, Severity: SeverityWarning, ExitCode: NoExit, Message: "empty rule scope"},
	{Code: CON025, Severity: SeverityInfo, ExitCode: NoExit, Message: "rule selects no target"},
	{Code: CON026, Severity: SeverityWarning, ExitCode: NoExit, Message: "language coverage not met: {language} {ratio} < {required}"},
	{Code: CON027, Severity: SeverityInfo, ExitCode: NoExit, Message: "rules not reducible to changed code, full scope reported: {rules}"},
	{Code: CON028, Severity: SeverityWarning, ExitCode: NoExit, Message: "byte coverage not met: {ratio} < {required}"},
	{Code: CON029, Severity: SeverityInfo, ExitCode: NoExit, Message: "the changed scope has no file"},
	{Code: CON030, Severity: SeverityWarning, ExitCode: NoExit, Message: "limit exceeded: {limit} is configured as {configured}, observed {observed}; dependent results are unknown, no evidence was cut"},
	{Code: CON031, Severity: SeverityInfo, ExitCode: NoExit, Message: "unknown because the family {family} is unsupported by the provider profile"},
	{Code: CON032, Severity: SeverityWarning, ExitCode: NoExit, Message: "predicate coverage not complete: {predicate} is {status} in {scope}: {detail}"},
	{Code: CON033, Severity: SeverityWarning, ExitCode: NoExit, Message: "edges skipped from the evaluation: {count} with {reason}"},
	{Code: CON099, Severity: SeverityError, ExitCode: Internal, Message: "rule evaluation error: {detail}"},
	{Code: EXC001, Severity: SeverityError, ExitCode: InvalidInput, Message: "baseline not valid: {detail}"},
	{Code: EXC002, Severity: SeverityError, ExitCode: InvalidInput, Message: "waiver not valid: {detail}"},
	{Code: EXC003, Severity: SeverityWarning, ExitCode: NoExit, Message: "orphaned waiver: {id} matches no current finding"},
	{Code: EXC004, Severity: SeverityError, ExitCode: InvalidInput, Message: "duplicate waiver ID: {id}"},
	{Code: EXC006, Severity: SeverityWarning, ExitCode: NoExit, Message: "waiver expired: {id} (expired {expires})"},
	{Code: EXC007, Severity: SeverityWarning, ExitCode: NoExit, Message: "waiver expires soon: {id} (expires {expires})"},
	{Code: EXC008, Severity: SeverityError, ExitCode: InvalidInput, Message: "baseline accept refused in CI: new findings never enter the baseline in CI"},
	{Code: EXC009, Severity: SeverityWarning, ExitCode: NoExit, Message: "scoped waiver not applied: {detail}"},
	{Code: EXC010, Severity: SeverityWarning, ExitCode: NoExit, Message: "scoped waiver matches no current finding: {detail}"},
	{Code: EXC011, Severity: SeverityWarning, ExitCode: NoExit, Message: "compensating evidence unresolved or digest differs: {detail}"},
	{Code: PRJ001, Severity: SeverityError, ExitCode: Toolchain, Message: "Structurizr CLI not available; it is needed for {format}", Suggestion: "install the Structurizr CLI (https://docs.structurizr.com/cli) and put structurizr-cli, structurizr.sh or structurizr on the PATH, or set STRUCTURIZR_CLI to its path; only the DSL output works without it"},
	{Code: PRJ002, Severity: SeverityError, ExitCode: AdapterFailure, Message: "Structurizr validate or export failed: {detail}"},
	{Code: PRJ005, Severity: SeverityError, ExitCode: InvalidInput, Message: "C4 level mapping not valid: {detail}"},
	{Code: PRJ006, Severity: SeverityInfo, ExitCode: NoExit, Message: "element not projected: {id} ({detail})"},
	{Code: PRJ007, Severity: SeverityWarning, ExitCode: NoExit, Message: "cannot open the output: {detail}"},
	{Code: PRJ008, Severity: SeverityWarning, ExitCode: NoExit, Message: "view not generated: {detail}"},
	{Code: DGM001, Severity: SeverityError, ExitCode: InvalidInput, Message: "view definition not valid: {view}: {detail}"},
	{Code: DGM002, Severity: SeverityError, ExitCode: InvalidInput, Message: "view output path not allowed: {view}: {path}: {detail}"},
	{Code: DGM003, Severity: SeverityError, ExitCode: InvalidInput, Message: "rendering input not allowed: {detail}"},
	{Code: DGM004, Severity: SeverityError, ExitCode: Toolchain, Message: "Structurizr CLI is not the pinned version: {detail}"},
	{Code: DGM005, Severity: SeverityInfo, ExitCode: NoExit, Message: "suggested view not declared: {detail}"},
	{Code: ADR001, Severity: SeverityError, ExitCode: InvalidInput, Message: "decision register not valid: {detail}"},
	{Code: ADR002, Severity: SeverityError, ExitCode: InvalidInput, Message: "duplicate registered decision ID: {id}"},
	{Code: ADR003, Severity: SeverityError, ExitCode: InvalidInput, Message: "governs reference unknown or selector empty: {detail}"},
	{Code: ADR004, Severity: SeverityError, ExitCode: InvalidInput, Message: "enforcedBy or released rule or pack version not loaded: {detail}"},
	{Code: ADR005, Severity: SeverityError, ExitCode: InvalidInput, Message: "lifecycle profile or trigger not valid: {detail}"},
	{Code: ADR006, Severity: SeverityWarning, ExitCode: NoExit, Message: "provider has no ADR capability, or revision unavailable: {detail}"},
	{Code: ADR007, Severity: SeverityInfo, ExitCode: NoExit, Message: "decision register configured but the adr-governance pack is not enabled"},
	{Code: ADR008, Severity: SeverityInfo, ExitCode: NoExit, Message: "unregistered or mention-based decision candidate: {detail}"},
	{Code: ADR009, Severity: SeverityInfo, ExitCode: NoExit, Message: "scope triggers configured but no change set: {detail}"},
	{Code: ASM001, Severity: SeverityError, ExitCode: InvalidInput, Message: "assessment document not valid: {detail}"},
	{Code: ASM002, Severity: SeverityError, ExitCode: InvalidInput, Message: "approved assessment version edited: {detail}"},
	{Code: ASM003, Severity: SeverityError, ExitCode: InvalidInput, Message: "authored state claims an approval: {detail}"},
	{Code: ASM004, Severity: SeverityError, ExitCode: InvalidInput, Message: "mandatory review rules need an authority and a trust anchor: {detail}"},
	{Code: ASM005, Severity: SeverityError, ExitCode: InvalidInput, Message: "assessment approve or revoke refused: {detail}"},
	{Code: ASM006, Severity: SeverityWarning, ExitCode: NoExit, Message: "approval not applied: {detail}"},
	{Code: ASM007, Severity: SeverityWarning, ExitCode: NoExit, Message: "trust anchor unreadable or not an ancestor of HEAD: {detail}"},
	{Code: ASM008, Severity: SeverityWarning, ExitCode: NoExit, Message: "incomplete change-impact analysis: {detail}"},
	{Code: ASM009, Severity: SeverityInfo, ExitCode: NoExit, Message: "traversal cycle seen: {detail}"},
	{Code: ASM010, Severity: SeverityInfo, ExitCode: NoExit, Message: "incremental cache not usable, full evaluation used: {detail}"},
	{Code: ASM011, Severity: SeverityWarning, ExitCode: NoExit, Message: "signature verification unavailable: {detail}"},
	{Code: SEC001, Severity: SeverityError, ExitCode: InvalidInput, Message: "trust declaration not valid: {detail}"},
	{Code: SEC002, Severity: SeverityWarning, ExitCode: NoExit, Message: "dependency kind unsupported by the capability profile: {detail}"},
	{Code: REP001, Severity: SeverityError, ExitCode: Internal, Message: "output write failed: {path}: {detail}"},
	{Code: REP002, Severity: SeverityError, ExitCode: Internal, Message: "SARIF serialization failed: {detail}"},
	{Code: REP003, Severity: SeverityError, ExitCode: Toolchain, Message: "bundle not signed: {detail}"},
	{Code: REP004, Severity: SeverityError, ExitCode: InvalidInput, Message: "bundle not valid: {detail}"},
	{Code: REP005, Severity: SeverityInfo, ExitCode: NoExit, Message: "redaction applied: {count} value(s) removed from {output}", Suggestion: "see docs/reference/redaction.md for the rules and the records"},
	{Code: REP006, Severity: SeverityError, ExitCode: InvalidInput, Message: "path refused: {path}: {detail}", Suggestion: "source files are read only below the workspace root and the declared project roots"},
	{Code: PRV001, Severity: SeverityError, ExitCode: InvalidInput, Message: "assurance declaration not valid: {detail}"},
	{Code: PRV002, Severity: SeverityError, ExitCode: InvalidInput, Message: "assurance path outside the workspace root: {path}"},
	{Code: PRV003, Severity: SeverityError, ExitCode: Toolchain, Message: "renderer missing, not pinned, or different from the pin: {detail}"},
	{Code: PRV004, Severity: SeverityError, ExitCode: Toolchain, Message: "OS sandbox required and unavailable: {detail}"},
	{Code: PRV005, Severity: SeverityWarning, ExitCode: NoExit, Message: "render failed, partial, or over a limit: {detail}"},
	{Code: PRV006, Severity: SeverityWarning, ExitCode: NoExit, Message: "embedded command or remote include ignored: {detail}"},
	{Code: PRV007, Severity: SeverityWarning, ExitCode: NoExit, Message: "duplicate resource identity in one render: {detail}"},
	{Code: PRV008, Severity: SeverityWarning, ExitCode: NoExit, Message: "plan format version unsupported or plan unparseable: {detail}"},
	{Code: PRV009, Severity: SeverityWarning, ExitCode: NoExit, Message: "statement malformed, signature failed, or subject digest mismatch: {detail}"},
	{Code: PRV010, Severity: SeverityInfo, ExitCode: NoExit, Message: "statement not admissible: {detail}"},
	{Code: PRV011, Severity: SeverityWarning, ExitCode: NoExit, Message: "evidence glob matches no file: {path}"},
	{Code: PRV012, Severity: SeverityWarning, ExitCode: NoExit, Message: "conflicting admissible results: {detail}"},
	{Code: PRV013, Severity: SeverityWarning, ExitCode: NoExit, Message: "evaluator input incomplete or construct unsupported: {detail}"},
	{Code: PRV014, Severity: SeverityWarning, ExitCode: NoExit, Message: "render output differs between two renders: {detail}"},
	{Code: QUA001, Severity: SeverityError, ExitCode: InvalidInput, Message: "quality scenario file not valid: {detail}"},
	{Code: QUA002, Severity: SeverityError, ExitCode: InvalidInput, Message: "quality scenario reference unresolved: {detail}"},
	{Code: QUA003, Severity: SeverityWarning, ExitCode: NoExit, Message: "measurement malformed or not admissible: {detail}"},
}
