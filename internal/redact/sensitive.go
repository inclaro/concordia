package redact

import (
	"regexp"
	"strings"
)

// camelBoundary finds the place between a lower-case letter or digit and an upper-case letter.
var camelBoundary = regexp.MustCompile(`([a-z0-9])([A-Z])`)

// nonWord splits a name into words.
var nonWord = regexp.MustCompile(`[^a-z0-9]+`)

const (
	wordSecret = "secret"
	wordKey    = "key"
)

var (
	sensitiveWords = map[string]struct{}{
		"password": {}, "passwd": {}, "passphrase": {}, "pwd": {}, wordSecret: {}, "secrets": {},
		"token": {}, "tokens": {}, "credential": {}, "credentials": {}, "apikey": {}, "privatekey": {},
		"accesskey": {}, "bearer": {}, "cookie": {},
	}

	// sensitivePairs are two words that are sensitive together, for example api_key.
	sensitivePairs = map[[2]string]struct{}{
		{"api", wordKey}: {}, {"private", wordKey}: {}, {"access", wordKey}: {}, {"auth", wordKey}: {}, {"signing", wordKey}: {},
		{"encryption", wordKey}: {}, {"master", wordKey}: {}, {wordSecret, wordKey}: {}, {"session", wordKey}: {},
		{"connection", "string"}: {}, {"shared", wordKey}: {},
	}

	// pointerWords end a name that refers to a secret and does not hold it: secretName, token_file.
	pointerWords = map[string]struct{}{
		"name": {}, "names": {}, "ref": {}, "file": {}, "path": {}, "dir": {}, "url": {}, "uri": {}, "namespace": {},
		"enabled": {}, "required": {}, "length": {}, "policy": {}, "type": {}, "mode": {}, "version": {}, "algorithm": {},
		"provider": {}, "store": {}, "class": {}, "kind": {}, "template": {}, "label": {}, "annotation": {}, "expiry": {},
		"rotation": {}, "ttl": {}, "audience": {}, "issuer": {},
	}
)

// strongParts are sensitive wherever they are in a word: PGPASSWORD, dbpassword, myapikey.
var strongParts = []string{"password", "passwd", "passphrase", "apikey", "privatekey", "accesskey", "secretkey", "credential"}

// weakSuffixes are sensitive at the end of a word: AUTHTOKEN, clientsecret, mysqlpwd.
var weakSuffixes = []string{"token", wordSecret, "tokens", "secrets", "pwd"}

func containsSensitive(word string) bool {
	for _, part := range strongParts {
		if strings.Contains(word, part) {
			return true
		}
	}

	for _, suffix := range weakSuffixes {
		if strings.HasSuffix(word, suffix) {
			return true
		}
	}

	return false
}

// nameWords splits a name such as `DB_PASSWORD`, `dbPassword`, or `db-password` into lower-case words.
func nameWords(name string) []string {
	spaced := strings.ToLower(camelBoundary.ReplaceAllString(name, "${1}_${2}"))

	var words []string

	for w := range strings.SplitSeq(nonWord.ReplaceAllString(spaced, " "), " ") {
		if w != "" {
			words = append(words, w)
		}
	}

	return words
}

// isPointerName says whether the name ends in a word that makes it a reference to a value (secretName,
// tokenFile, secretKeyRef) and not the value.
func isPointerName(name string) bool {
	words := nameWords(name)
	if len(words) == 0 {
		return false
	}

	_, pointer := pointerWords[words[len(words)-1]]

	return pointer
}

// sensitiveName says whether the name (of an environment variable, a key, a property, a
// variable) names a secret value. A name that ends in a word like `name`, `ref`, `file`, or
// `path` names a reference to a secret, which stays.
func sensitiveName(name string) bool {
	words := nameWords(name)
	if len(words) == 0 {
		return false
	}

	if _, pointer := pointerWords[words[len(words)-1]]; pointer {
		return false
	}

	if name == "PWD" || name == "OLDPWD" {
		return false
	}

	for i, w := range words {
		if _, ok := sensitiveWords[w]; ok || containsSensitive(w) {
			return true
		}

		if i+1 < len(words) {
			if _, ok := sensitivePairs[[2]string{w, words[i+1]}]; ok {
				return true
			}
		}
	}

	return false
}

var (
	shellReference = regexp.MustCompile(`^\$(?:[A-Za-z_][A-Za-z0-9_]*|\{[A-Za-z_][A-Za-z0-9_]*(?::?[-+=?]([^}]*))?\}|\(.*\))$`)
	templateRef    = regexp.MustCompile(`^\{\{.*\}\}$`)
)

// isReference says whether a value is a reference to a value somewhere else (`${DB_PASSWORD}`,
// `$(vault read x)`, `{{ .Values.x }}`) and not a value. A reference holds no secret.
func isReference(value string) bool {
	v := strings.TrimSpace(value)

	if m := shellReference.FindStringSubmatch(v); m != nil {
		// `${X:-word}` with a literal word is a default value, not a reference: the word can be a secret.
		// An empty word, or a word that is itself a reference, holds none.
		return m[1] == "" || isReference(m[1])
	}

	return templateRef.MatchString(v)
}

// isRedactable says whether a value is worth a marker: it is not empty, not a reference, and not a marker.
func isRedactable(value string) bool {
	v := strings.TrimSpace(value)

	return v != "" && !isReference(v) && !strings.Contains(v, markerPrefix)
}
