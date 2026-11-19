// Package redact removes secret values from text before Concordia shows or stores it, and
// neutralizes terminal control sequences in text that comes from repository content.
//
// Redaction is deterministic and irreversible: a marker `[REDACTED:<class>]` replaces the value
// and carries nothing of it (no hash, no prefix, no length, not even the number of lines of a
// multi-line value). A Record says where a marker is and of which class; it never holds the value.
//
// Which passes run depends on the file: structure-aware passes for YAML and JSON (Kubernetes,
// Compose, Helm values), line passes for Dockerfile, Terraform variable files, and shell scripts,
// and for every file a pattern pass for known token shapes, URL credentials, and private keys.
package redact

import (
	"path"
	"strings"
)

// Version is the version of the rule set. The bundle manifest records it. It changes when a
// rule can redact a value that an earlier version left in place, or the reverse.
const Version = "1"

// Classes of redacted values.
const (
	// ClassSecretData is a value under a sensitive key: Kubernetes Secret data, a password in a
	// variable file, a sensitive property.
	ClassSecretData = "secret-data"
	// ClassEnvValue is the value of an environment variable with a sensitive name.
	ClassEnvValue = "env-value"
	// ClassToken is a value of a known token shape (GitHub, AWS, Slack, JWT, bearer header).
	ClassToken = "token"
	// ClassPrivateKey is a PEM private key block.
	ClassPrivateKey = "private-key"
	// ClassCredential is the password of a URL such as postgres://user:password@host.
	ClassCredential = "credential"
	// ClassOutsideContract is a field or facet that an output does not keep because its contract does not list it:
	// a field of a persisted statement outside the contract of its predicate type, or a facet of a review item outside
	// the digest contract. It is not a secret class: the value is dropped because it is not part of the output, and
	// the record says so without claiming that the value was secret.
	ClassOutsideContract = "outside-contract"
)

const markerPrefix = "[REDACTED:"

// Marker is the text that replaces a value of the class.
func Marker(class string) string { return markerPrefix + class + "]" }

// Rules is a redaction rule set. Use Default.
type Rules struct {
	// Version is the version of the rule set.
	Version string
}

// Default is the rule set of this version of Concordia.
func Default() Rules { return Rules{Version: Version} }

// Record says that one value was redacted. It never holds the value or a part of it.
type Record struct {
	// Output names the output that holds the marker (for example `snippets/ab12.txt` or
	// `explain`). Redact leaves it empty: the caller knows the output.
	Output string `json:"output,omitempty"`
	// File is the path that Redact was given, empty for a property value.
	File string `json:"file,omitempty"`
	// Property is the key or variable name whose value was redacted, when the pass knows it.
	Property string `json:"property,omitempty"`
	// Class is one of the Class constants.
	Class string `json:"class"`
	// Line is the 1-based line in the original text where the value starts, 0 when there is none.
	Line int `json:"line,omitempty"`
}

// Line is one line of redacted text with the number of the line in the original text. A value
// that spans several lines becomes one line, so numbers can skip.
type Line struct {
	Text   string
	Number int
}

// Redact removes the secret values from the text of the file at path and gives the redacted
// text with one Record for each value. file chooses the passes (by file name) and is only used
// as the File of the records. It must be the whole file, or at least whole instructions: a
// snippet that starts inside a PEM block or a multi-line value cannot be recognized. To show
// a few lines of a file, use RedactLines on the whole file and keep the lines you need.
//
// The same input gives the same output and the same records. Line endings are kept.
func (r Rules) Redact(file, content string) (string, []Record) {
	buf := r.run(file, content)

	return buf.text(), buf.records
}

// RedactLines is Redact with numbered output lines: the result of the whole file, each line with
// its number in the original text, so the caller can select a window of the original numbers.
func (r Rules) RedactLines(file, content string) ([]Line, []Record) {
	buf := r.run(file, content)

	return buf.numbered(), buf.records
}

// RedactValue removes a secret from one property value. The value is redacted as a whole when
// the name is a sensitive name (`password`, `API_TOKEN`), else the pattern pass looks for token
// shapes and private keys in it. The property name stays.
func (r Rules) RedactValue(name, value string) (string, []Record) {
	if sensitiveName(name) && isRedactable(value) {
		return Marker(ClassSecretData), []Record{{Property: name, Class: ClassSecretData}}
	}

	buf := newBuffer("", value)
	patternPass(buf)

	for i := range buf.records {
		buf.records[i].Property = name
		buf.records[i].Line = 0
	}

	return buf.text(), buf.records
}

func (r Rules) run(file, content string) *buffer {
	buf := newBuffer(file, content)

	base := strings.ToLower(path.Base(strings.ReplaceAll(file, `\`, "/")))

	switch kindOf(base, content) {
	case kindYAML:
		yamlPass(buf)
	case kindDockerfile:
		dockerfilePass(buf)
	case kindVariables:
		keyValuePass(buf, 0, len(buf.lines), true)
	case kindShell:
		shellPass(buf)
	case kindOther:
	}

	patternPass(buf)

	stableSortRecords(buf.records)

	return buf
}

type fileKind int

const (
	kindOther fileKind = iota
	kindYAML
	kindDockerfile
	kindVariables
	kindShell
)

func kindOf(base, content string) fileKind {
	switch {
	case strings.HasSuffix(base, ".yaml"), strings.HasSuffix(base, ".yml"), strings.HasSuffix(base, ".json"):
		return kindYAML
	case base == "dockerfile", strings.HasPrefix(base, "dockerfile."), strings.HasSuffix(base, ".dockerfile"),
		base == "containerfile", strings.HasPrefix(base, "containerfile."), strings.HasSuffix(base, ".containerfile"):
		return kindDockerfile
	case strings.HasSuffix(base, ".tfvars"):
		return kindVariables
	case strings.HasSuffix(base, ".sh"), strings.HasSuffix(base, ".bash"), strings.HasSuffix(base, ".zsh"), strings.HasSuffix(base, ".ksh"),
		base == ".env", strings.HasPrefix(base, ".env."), strings.HasSuffix(base, ".env"), base == ".envrc",
		base == ".bashrc", base == ".zshrc", base == ".profile", strings.HasPrefix(content, "#!") && isShellShebang(content):
		return kindShell
	}

	return kindOther
}

func isShellShebang(content string) bool {
	first, _, _ := strings.Cut(content, "\n")

	for _, shell := range []string{"sh", "bash", "zsh", "ksh", "dash", "ash"} {
		if strings.HasSuffix(strings.TrimSpace(first), "/"+shell) || strings.HasSuffix(strings.TrimSpace(first), " "+shell) {
			return true
		}
	}

	return false
}
