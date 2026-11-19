package redact_test

// ruleCase is one input of TestRedactRules: the file name, the text, the redacted text that
// Redact must give, and the number of records.
type ruleCase struct {
	name, file, in, want string
	count                int
}

// dockerRuleCases are the cases of Dockerfiles.
var dockerRuleCases = []ruleCase{
	{
		name: "Dockerfile ENV with a sensitive name", file: "Dockerfile", count: 1,
		in:   "FROM x\nENV API_TOKEN=abc123\n",
		want: "FROM x\nENV API_TOKEN=[REDACTED:env-value]\n",
	},
	{
		name: "Dockerfile ENV several pairs, legacy form, ARG, continuation", file: "build/app.dockerfile", count: 4,
		in:   "ENV A=1 DB_PASSWORD=\"s e c\" B=2\nENV SECRET_KEY some long value\nARG npm_token=xyz\nENV X=1 \\\n    PASSWD=p \\\n    Y=2\n",
		want: "ENV A=1 DB_PASSWORD=[REDACTED:env-value] B=2\nENV SECRET_KEY [REDACTED:env-value]\nARG npm_token=[REDACTED:env-value]\nENV X=1 \\\n    PASSWD=[REDACTED:env-value] \\\n    Y=2\n",
	},
	{
		name: "Dockerfile references and pointers stay", file: "Dockerfile", count: 0,
		in:   "ENV API_TOKEN=${API_TOKEN}\nENV PASSWORD_FILE=/run/secrets/pw\nENV LISTEN=:8080\nARG VERSION\n",
		want: "ENV API_TOKEN=${API_TOKEN}\nENV PASSWORD_FILE=/run/secrets/pw\nENV LISTEN=:8080\nARG VERSION\n",
	},
	{
		name: "Dockerfile RUN with a token flag and a header", file: "Dockerfile", count: 2,
		in:   "RUN curl --token abc -H \"Authorization: Bearer abcdefgh12345\" https://x\n",
		want: "RUN curl --token [REDACTED:secret-data] -H \"Authorization: Bearer [REDACTED:token]\" https://x\n",
	},
}

// scriptRuleCases are the cases of Terraform variable files, dotenv files, and shell scripts.
var scriptRuleCases = []ruleCase{
	{
		name: "tfvars string, list, object, heredoc", file: "terraform/prod.tfvars", count: 4,
		in: "db_password = \"p1\"\nregion = \"eu\"\napi_tokens = [\n  \"a\",\n  \"b\",\n]\n" +
			"creds = { user = \"u\", password = \"p2\" }\nprivate_key = <<EOT\nline1\nline2\nEOT\nafter = 1\n",
		want: "db_password = [REDACTED:secret-data]\nregion = \"eu\"\napi_tokens = [REDACTED:secret-data]\n" +
			"creds = { user = \"u\", password = [REDACTED:secret-data] }\nprivate_key = [REDACTED:secret-data]\nafter = 1\n",
	},
	{
		name: "shell assignments, flags, URL credential", file: "scripts/run.sh", count: 5,
		in: "#!/bin/sh\nexport DB_PASSWORD='p w'\nPGPASSWORD=abc psql\ndocker run -e API_KEY=k1 img --password secret1 --name web\n" +
			"curl https://user:pw123@host/x\n",
		want: "#!/bin/sh\nexport DB_PASSWORD=[REDACTED:env-value]\nPGPASSWORD=[REDACTED:env-value] psql\n" +
			"docker run -e API_KEY=[REDACTED:env-value] img --password [REDACTED:secret-data] --name web\ncurl https://user:[REDACTED:credential]@host/x\n",
	},
	{
		name: "shell references stay", file: "x.sh", count: 0,
		in:   "export TOKEN=\"$TOKEN\"\nexport PW=$(vault read pw)\nexport A_SECRET=${A_SECRET:-}\n",
		want: "export TOKEN=\"$TOKEN\"\nexport PW=$(vault read pw)\nexport A_SECRET=${A_SECRET:-}\n",
	},
	{
		name: "dotenv file", file: ".env.production", count: 1,
		in:   "NODE_ENV=production\nDATABASE_PASSWORD=hunter2\n",
		want: "NODE_ENV=production\nDATABASE_PASSWORD=[REDACTED:env-value]\n",
	},
	{
		name: "script by shebang", file: "bin/deploy", count: 1,
		in:   "#!/usr/bin/env bash\nTOKEN=abc\n",
		want: "#!/usr/bin/env bash\nTOKEN=[REDACTED:env-value]\n",
	},
}

// yamlRuleCases are the cases of Kubernetes, Compose, Helm, and JSON documents.
var yamlRuleCases = []ruleCase{
	{
		name: "Kubernetes env, secretKeyRef stays", file: "d.yaml", count: 1,
		in: "env:\n  - name: DB_PASSWORD\n    value: \"hunter2\"   # note\n  - name: DB_USER\n    value: admin\n" +
			"  - name: TOKEN\n    valueFrom:\n      secretKeyRef:\n        name: db-credentials\n        key: token\n",
		want: "env:\n  - name: DB_PASSWORD\n    value: [REDACTED:env-value]   # note\n  - name: DB_USER\n    value: admin\n" +
			"  - name: TOKEN\n    valueFrom:\n      secretKeyRef:\n        name: db-credentials\n        key: token\n",
	},
	{
		name: "Compose environment mapping and list", file: "docker-compose.yml", count: 3,
		in: "services:\n  a:\n    environment:\n      POSTGRES_PASSWORD: pw\n      MODE: fast\n      API_KEY: 'k'\n" +
			"  b:\n    environment:\n      - DB_PASSWORD=pw2\n      - MODE=fast\n    secrets:\n      - db_password\n",
		want: "services:\n  a:\n    environment:\n      POSTGRES_PASSWORD: [REDACTED:env-value]\n      MODE: fast\n      API_KEY: [REDACTED:env-value]\n" +
			"  b:\n    environment:\n      - DB_PASSWORD=[REDACTED:env-value]\n      - MODE=fast\n    secrets:\n      - db_password\n",
	},
	{
		name: "Secret with block scalar, multi-document, last-applied annotation", file: "s.yaml", count: 4,
		in: "kind: Secret\nmetadata:\n  name: a\n  annotations:\n    kubectl.kubernetes.io/last-applied-configuration: |\n      {\"data\":{\"k\":\"v\"}}\n" +
			"stringData:\n  cert: |\n    line1\n    line2\n  other: \"q\\\"uote\"\n---\nkind: ConfigMap\ndata:\n  mode: fast\n---\nkind: Secret\ndata:\n  k: dg==\n",
		want: "kind: Secret\nmetadata:\n  name: a\n  annotations:\n    kubectl.kubernetes.io/last-applied-configuration: [REDACTED:secret-data]\n" +
			"stringData:\n  cert: [REDACTED:secret-data]\n  other: [REDACTED:secret-data]\n---\nkind: ConfigMap\ndata:\n  mode: fast\n---\nkind: Secret\ndata:\n  k: [REDACTED:secret-data]\n",
	},
	{
		name: "Secret in a List", file: "list.json", count: 1,
		in:   "{\"kind\":\"List\",\"items\":[{\"kind\":\"Secret\",\"data\":{\"k\":\"dg==\"},\"metadata\":{\"name\":\"s\"}}]}",
		want: "{\"kind\":\"List\",\"items\":[{\"kind\":\"Secret\",\"data\":{\"k\":[REDACTED:secret-data]},\"metadata\":{\"name\":\"s\"}}]}",
	},
	{
		name: "generic sensitive key in values, pointers stay", file: "charts/values.yaml", count: 3,
		in:   "db:\n  password: hunter2\n  passwordSecret: x\n  secretName: db-credentials\n  tokenFile: /t\n  host: h\napiKey: abc\n",
		want: "db:\n  password: [REDACTED:secret-data]\n  passwordSecret: [REDACTED:secret-data]\n  secretName: db-credentials\n  tokenFile: /t\n  host: h\napiKey: [REDACTED:secret-data]\n",
	},
	{
		name: "list and mapping under a sensitive key, pointers inside stay", file: "charts/values.yaml", count: 5,
		in: "apiTokens:\n  - abc123\n  - def456\nflowTokens: [a1, b2]\n" +
			"credentials:\n  user: u\n  secretName: s\n",
		want: "apiTokens:\n  - [REDACTED:secret-data]\n  - [REDACTED:secret-data]\nflowTokens: [[REDACTED:secret-data], [REDACTED:secret-data]]\n" +
			"credentials:\n  user: [REDACTED:secret-data]\n  secretName: s\n",
	},
	{
		name: "non-ASCII text before a value", file: "u.yaml", count: 1,
		in:   "env:\n  - {name: \"é-PASSWORD\", value: \"ünï\"}\n  - name: X\n    value: \"é\"\n",
		want: "env:\n  - {name: \"é-PASSWORD\", value: [REDACTED:env-value]}\n  - name: X\n    value: \"é\"\n",
	},
	{
		name: "YAML that does not parse falls back to key lines", file: "t.yaml", count: 1,
		in:   "a: [unclosed\npassword: hunter2 # c\n",
		want: "a: [unclosed\npassword: [REDACTED:secret-data] # c\n",
	},
}

// textRuleCases are the cases of token shapes, shell expansions, PEM blocks, and values with
// nothing to remove.
var textRuleCases = []ruleCase{
	{
		name: "token shapes in a plain file", file: "notes.txt", count: 5,
		in: "gh ghp_CONCORDIASEEDshelltoken0000000000000\naws AKIAABCDEFGHIJKLMNOP\nslack xoxb-1234567890-abcdefghij\n" +
			"jwt eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0In0.abcdefghijk\nAuthorization: Basic dXNlcjpwYXNz\n",
		want: "gh [REDACTED:token]\naws [REDACTED:token]\nslack [REDACTED:token]\njwt [REDACTED:token]\nAuthorization: Basic [REDACTED:token]\n",
	},
	{
		name: "expansion with a literal default is a value, an empty or reference default is kept", file: "d.sh", count: 1,
		in:   "export DB_PASSWORD=${DB_PASSWORD:-hunter2}\nexport A_SECRET=${A_SECRET:?}\nexport B_SECRET=${B_SECRET:-$OTHER}\n",
		want: "export DB_PASSWORD=[REDACTED:env-value]\nexport A_SECRET=${A_SECRET:?}\nexport B_SECRET=${B_SECRET:-$OTHER}\n",
	},
	{
		name: "PEM block in a one-line JSON string and unterminated block", file: "k.json", count: 1,
		in:   "{\"k\":\"-----BEGIN PRIVATE KEY-----\\nMIIE\\n-----END PRIVATE KEY-----\\n\",\"n\":1}",
		want: "{\"k\":\"[REDACTED:private-key]\\n\",\"n\":1}",
	},
	{
		name: "two PEM blocks on one line are both redacted", file: "k.json", count: 2,
		in:   "{\"a\":\"-----BEGIN PRIVATE KEY-----\\nAAAA\\n-----END PRIVATE KEY-----\",\"b\":\"-----BEGIN RSA PRIVATE KEY-----\\nBBBB\\n-----END RSA PRIVATE KEY-----\"}",
		want: "{\"a\":\"[REDACTED:private-key]\",\"b\":\"[REDACTED:private-key]\"}",
	},
	{
		name: "unterminated PEM block runs to the end", file: "k.txt", count: 1,
		in:   "a\n-----BEGIN OPENSSH PRIVATE KEY-----\nAAAA\nBBBB\n",
		want: "a\n[REDACTED:private-key]\n",
	},
	{
		name: "certificates are public", file: "c.pem", count: 0,
		in:   "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n",
		want: "-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n",
	},
	{
		name: "empty and null values have nothing to remove", file: "e.yaml", count: 0,
		in:   "password: \"\"\ntoken:\nsecret: null\nenabled: true\n",
		want: "password: \"\"\ntoken:\nsecret: null\nenabled: true\n",
	},
}
