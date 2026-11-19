package observation_test

import (
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/observation"
)

// The endpoints that the rows of the tests share.
var (
	epFunction   = endpoint{label: "Function", file: "src/a.go"}
	epGoFile     = endpoint{label: "File", file: "src/a.go"}
	epDockerfile = endpoint{label: "Module", file: "docker/Dockerfile"}
	epScript     = endpoint{label: "Module", file: "scripts/deploy.sh"}
	epWorkload   = endpoint{label: "Resource", file: "k8s/deployment.yaml", kind: "Deployment"}
	epService    = endpoint{label: "Resource", file: "k8s/service.yaml", kind: "Service"}
	epSecret     = endpoint{label: "Resource", file: "k8s/secret.yaml", kind: "Secret"}
	epChart      = endpoint{label: "Chart", file: "charts/api/Chart.yaml"}
	epImage      = endpoint{label: "Image"}
	epTerraform  = endpoint{label: "Class", file: "terraform/main.tf"}
	epEnvVar     = endpoint{label: "EnvVar"}
	epSection    = endpoint{label: "Section", file: "docs/a.md"}
	epADR        = endpoint{label: "ADR", file: "docs/adr/0002.md"}
	epElement    = endpoint{label: "DiagramElement", file: "docs/a.md"}
	epMarkdown   = endpoint{label: "File", file: "docs/a.md"}
	epKustomize  = endpoint{label: "File", file: "kustomize/base/kustomization.yaml"}
	epYAMLFile   = endpoint{label: "File", file: "config/app.yaml"}
)

func normExpectProperties(t *testing.T, got, want map[string]string) {
	t.Helper()

	for k, v := range want {
		if got[k] != v {
			t.Fatalf("property %s = %q, want %q (kept %q)", k, got[k], v, got[k])
		}
	}
}

// normRuleRow is a row of the evidence-normalization table: a native edge shape and the fact it gives.
type normRuleRow struct {
	name    string
	native  string
	props   map[string]string
	from    endpoint
	to      endpoint
	kind    observation.CanonicalEdgeKind
	facets  map[string]string
	role    observation.EvidenceRole
	resolve observation.Resolution
}

// The rows of the evidence-normalization table: a native edge shape and the fact it gives.
func TestNormalizationRuleRows(t *testing.T) {
	rows := slices.Concat(normDeploymentRows(), normKubernetesUsageRows(), normTerraformConfigRows(), normDocumentRows(), normAdvisoryRows())

	for _, r := range rows {
		t.Run(r.name, func(t *testing.T) {
			e := normBetween(t, &normCase{native: r.native, props: r.props, from: r.from, to: r.to})

			normExpectRuleRowFact(t, e, &r)

			if !reflect.DeepEqual(e.Facets, r.facets) {
				t.Fatalf("facets = %v, want %v", e.Facets, r.facets)
			}

			gotLabels := [3]string{e.SourceLabel, e.TargetLabel, e.NormalizationVersion}
			if gotLabels != [3]string{r.from.label, r.to.label, "2.1"} {
				t.Fatalf("labels %s -> %s, version %q", e.SourceLabel, e.TargetLabel, e.NormalizationVersion)
			}

			// The native properties stay as they were, whether or not a rule used them.
			normExpectProperties(t, e.Properties, r.props)
		})
	}
}

func normExpectRuleRowFact(t *testing.T, e *observation.EvidenceEdge, r *normRuleRow) {
	t.Helper()

	got := [4]string{string(e.Kind), string(e.Role), string(e.Resolution), e.NativeKind}
	if got != [4]string{string(r.kind), string(r.role), string(r.resolve), r.native} {
		t.Fatalf("edge = kind %s role %s resolution %s native %s", e.Kind, e.Role, e.Resolution, e.NativeKind)
	}
}

// normDeploymentRows are the rows of the deployment, dependency, mapping, launch, override, and import facts.
func normDeploymentRows() []normRuleRow {
	return []normRuleRow{
		{"deploys via kubectl targets a file", "DEPLOYS", map[string]string{"via": "kubectl", "path": "k8s/a.yaml"}, epScript, epGoFile, "deploys-artifact", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"deploys via helm", "DEPLOYS", map[string]string{"via": "helm", "chart": "charts/api"}, epScript, epChart, "deploys-artifact", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"deploys via chart", "DEPLOYS", map[string]string{"via": "chart"}, epChart, epWorkload, "deploys-artifact", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"depends-on from", "DEPENDS_ON", map[string]string{"via": "from", "image": "golang:1.22", "stage": "0"}, epDockerfile, epImage, "base-image", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"depends-on copy-from", "DEPENDS_ON", map[string]string{"via": "copy-from", "image": "x:1"}, epDockerfile, epImage, "copies-from-image", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"depends-on build", "DEPENDS_ON", map[string]string{"via": "build", "tool": "docker-build"}, epImage, epDockerfile, "built-from", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"depends-on depends_on is compose", "DEPENDS_ON", map[string]string{"via": "depends_on"}, epWorkload, epService, "infrastructure-dependency", map[string]string{"family": "compose"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"chart dependency has no via", "DEPENDS_ON", map[string]string{"repository": "https://charts.example.com/stable"}, epChart, epChart, "infrastructure-dependency", map[string]string{"family": "chart"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"infra-maps selector", "INFRA_MAPS", map[string]string{"kind": "selector", "workload": "api"}, epService, epWorkload, "selects-workload", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"infra-maps ingress", "INFRA_MAPS", map[string]string{"kind": "ingress"}, epWorkload, epService, "routes-to", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"infra-maps route", "INFRA_MAPS", map[string]string{"kind": "route"}, epWorkload, epService, "routes-to", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"infra-maps httproute", "INFRA_MAPS", map[string]string{"kind": "httproute"}, endpoint{label: "Route", file: "k8s/r.yaml"}, epService, "routes-to", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"connects-to env", "CONNECTS_TO", map[string]string{"via": "env", "key": "DB_URL", "container": "api"}, epService, epService, "configured-connection", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"spawns shell", "SPAWNS", map[string]string{"api": "shell", "ci": "github-actions", "job": "build"}, endpoint{label: "Module", file: ".github/workflows/ci.yaml"}, epScript, "launches", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"spawns dockerfile", "SPAWNS", map[string]string{"api": "dockerfile", "instruction": "ENTRYPOINT"}, epDockerfile, epFunction, "launches", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"override tfvars", "OVERRIDE", map[string]string{"via": "tfvars"}, endpoint{label: "Variable", file: "terraform/prod.tfvars"}, epTerraform, "overrides", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"override patch", "OVERRIDE", map[string]string{"via": "patch"}, epWorkload, epWorkload, "overrides", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"imports via terraform", "IMPORTS", map[string]string{"via": "terraform", "local_name": "network"}, epTerraform, endpoint{label: "Folder"}, "includes-infrastructure", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"imports of kustomization.yaml", "IMPORTS", map[string]string{"local_name": "resources"}, epKustomize, endpoint{label: "Module", file: "kustomize/base/deployment.yaml"}, "includes-infrastructure", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"imports of code", "IMPORTS", map[string]string{"local_name": "b"}, epGoFile, epFunction, "imports", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
	}
}

// normKubernetesUsageRows are the rows of the `USAGE` edges between Kubernetes resources.
func normKubernetesUsageRows() []normRuleRow {
	return []normRuleRow{
		{"usage image", "USAGE", map[string]string{"ref": "image", "container": "api", "image": "r/api:1.0"}, epWorkload, epImage, "uses-image", map[string]string{"referenceMode": "image"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage serviceAccountName", "USAGE", map[string]string{"ref": "serviceAccountName"}, epWorkload, endpoint{label: "Resource", file: "k8s/rbac.yaml", kind: "ServiceAccount"}, "uses-runtime-identity", map[string]string{"referenceMode": "serviceAccountName"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage env of a Secret", "USAGE", map[string]string{"ref": "env"}, epWorkload, endpoint{label: "Resource", kind: "Secret"}, "references-secret", map[string]string{"referenceMode": "env", "targetKind": "Secret"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage envFrom of a Secret", "USAGE", map[string]string{"ref": "envFrom"}, epWorkload, endpoint{label: "Resource", kind: "Secret"}, "references-secret", map[string]string{"referenceMode": "envFrom", "targetKind": "Secret"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage envFrom of a ConfigMap", "USAGE", map[string]string{"ref": "envFrom"}, epWorkload, endpoint{label: "Resource", kind: "ConfigMap"}, "references-configuration", map[string]string{"referenceMode": "envFrom", "targetKind": "ConfigMap"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage volume of a Secret", "USAGE", map[string]string{"ref": "volume"}, epWorkload, endpoint{label: "Resource", kind: "Secret"}, "references-secret", map[string]string{"referenceMode": "volume", "targetKind": "Secret"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage volume of a ConfigMap", "USAGE", map[string]string{"ref": "volume"}, epWorkload, endpoint{label: "Resource", kind: "ConfigMap"}, "references-configuration", map[string]string{"referenceMode": "volume", "targetKind": "ConfigMap"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage volume of a claim", "USAGE", map[string]string{"ref": "volume"}, epWorkload, endpoint{label: "Resource", kind: "PersistentVolumeClaim"}, "references-storage", map[string]string{"referenceMode": "volume", "targetKind": "PersistentVolumeClaim"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage tls", "USAGE", map[string]string{"ref": "tls"}, epWorkload, endpoint{label: "Resource", kind: "Secret"}, "references-certificate-secret", map[string]string{"referenceMode": "tls"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage parentRef", "USAGE", map[string]string{"ref": "parentRef"}, epWorkload, epWorkload, "route-attaches-to-gateway", map[string]string{"referenceMode": "parentRef"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage roleRef", "USAGE", map[string]string{"ref": "roleRef"}, epWorkload, epWorkload, "binding-refers-to-role", map[string]string{"referenceMode": "roleRef"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage subject", "USAGE", map[string]string{"ref": "subject"}, epWorkload, epWorkload, "binding-applies-to-principal", map[string]string{"referenceMode": "subject"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage podSelector", "USAGE", map[string]string{"ref": "podSelector"}, epWorkload, epWorkload, "policy-selects-workload", map[string]string{"referenceMode": "podSelector"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage selector", "USAGE", map[string]string{"ref": "selector"}, epWorkload, epWorkload, "disruption-policy-target", map[string]string{"referenceMode": "selector"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage scaleTarget", "USAGE", map[string]string{"ref": "scaleTarget"}, epWorkload, epWorkload, "scaling-target", map[string]string{"referenceMode": "scaleTarget"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
	}
}

// normTerraformConfigRows are the rows of the Terraform and code `USAGE` edges and of the `CONFIGURES` edges.
func normTerraformConfigRows() []normRuleRow {
	return []normRuleRow{
		{"usage terraform var", "USAGE", map[string]string{"via": "terraform", "ref": "var"}, epTerraform, epTerraform, "infrastructure-dependency", map[string]string{"family": "terraform", "terraformRef": "var"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage terraform module", "USAGE", map[string]string{"via": "terraform", "ref": "module"}, epTerraform, epTerraform, "infrastructure-dependency", map[string]string{"family": "terraform", "terraformRef": "module"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage terraform output", "USAGE", map[string]string{"via": "terraform", "ref": "output"}, epTerraform, epTerraform, "infrastructure-dependency", map[string]string{"family": "terraform", "terraformRef": "output"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage terraform resource", "USAGE", map[string]string{"via": "terraform", "ref": "resource"}, epTerraform, epTerraform, "infrastructure-dependency", map[string]string{"family": "terraform", "terraformRef": "resource"}, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"usage of code has only callee", "USAGE", map[string]string{"callee": "main"}, epGoFile, epFunction, "ambiguous-usage", nil, observation.RoleInferredAssociation, observation.ResolutionInferred},
		{"configures env_access", "CONFIGURES", map[string]string{"strategy": "env_access"}, epFunction, epEnvVar, "configures-env-access", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"configures env_set", "CONFIGURES", map[string]string{"strategy": "env_set", "scope": "container", "container": "app", "source": "value", "line": "23"}, epWorkload, epEnvVar, "configures-env-set", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"configures env_read", "CONFIGURES", map[string]string{"strategy": "env_read", "dockerfile": "docker/Dockerfile"}, endpoint{label: "EnvVar", file: "compose.yaml"}, epFunction, "configures-env-read", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"configures key_symbol is inferred", "CONFIGURES", map[string]string{"strategy": "key_symbol"}, endpoint{label: "File", file: "config.yaml"}, epFunction, "configures-key-symbol", nil, observation.RoleInferredAssociation, observation.ResolutionInferred},
		{"configures helm_values", "CONFIGURES", map[string]string{"strategy": "helm_values"}, endpoint{label: "File", file: "charts/api/values.yaml"}, epChart, "configures-helm-values", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
	}
}

// normDocumentRows are the rows of the documentation and diagram facts.
func normDocumentRows() []normRuleRow {
	return []normRuleRow{
		{"supersedes", "SUPERSEDES", map[string]string{"via": "markdown", "syntax": "supersedes", "tier": "exact", "count": "1", "superseded_by": "0002.md"}, epADR, epADR, "supersedes", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
		{"mentions link", "MENTIONS", map[string]string{"syntax": "link", "tier": "exact", "via": "markdown", "count": "1"}, epSection, epMarkdown, "mentions", nil, observation.RoleInferredAssociation, observation.ResolutionInferred},
		{"mentions code_path", "MENTIONS", map[string]string{"syntax": "code_path"}, epSection, epMarkdown, "mentions", nil, observation.RoleInferredAssociation, observation.ResolutionInferred},
		{"references-file md_inline_link", "REFERENCES_FILE", map[string]string{"strategy": "md_inline_link", "confidence": "0.9"}, epMarkdown, epGoFile, "references-file", nil, observation.RoleInferredAssociation, observation.ResolutionInferred},
		{"references-file md_backtick_path", "REFERENCES_FILE", map[string]string{"strategy": "md_backtick_path"}, epMarkdown, epGoFile, "references-file", nil, observation.RoleInferredAssociation, observation.ResolutionInferred},
		{"diagram arrow", "DIAGRAM_ARROW", map[string]string{"label": "sql", "style": "arrow"}, epElement, epElement, "diagram-arrow", nil, observation.RoleParsedSourceFact, observation.ResolutionExact},
	}
}

// normAdvisoryRows are the rows of the candidate and advisory associations.
func normAdvisoryRows() []normRuleRow {
	return []normRuleRow{
		{"tests is a candidate verification artifact", "TESTS", nil, epFunction, epFunction, "tests", nil, observation.RoleCandidateVerificationArtifact, observation.ResolutionInferred},
		{"tests-file is a candidate verification artifact", "TESTS_FILE", nil, epGoFile, epGoFile, "tests-file", nil, observation.RoleCandidateVerificationArtifact, observation.ResolutionInferred},
		{"similar-to is advisory", "SIMILAR_TO", nil, epFunction, epFunction, "similar-to", nil, observation.RoleInferredAssociation, observation.ResolutionInferred},
		{"semantically-related is advisory", "SEMANTICALLY_RELATED", nil, epFunction, epFunction, "semantically-related", nil, observation.RoleInferredAssociation, observation.ResolutionInferred},
		{"file-changes-with is advisory", "FILE_CHANGES_WITH", nil, epGoFile, epGoFile, "file-changes-with", nil, observation.RoleInferredAssociation, observation.ResolutionInferred},
		{"grpc calls", "GRPC_CALLS", nil, epFunction, epFunction, "calls-grpc", nil, observation.RoleInferredAssociation, observation.ResolutionInferred},
		{"cross graphql calls", "CROSS_GRAPHQL_CALLS", nil, epFunction, epFunction, "calls-graphql", nil, observation.RoleInferredAssociation, observation.ResolutionInferred},
		{"trpc calls", "TRPC_CALLS", nil, epFunction, epFunction, "calls-trpc", nil, observation.RoleInferredAssociation, observation.ResolutionInferred},
	}
}

// normAmbiguousRow is a native edge shape that no rule accepts, with the reason that the ambiguity facet gives.
type normAmbiguousRow struct {
	name   string
	native string
	props  map[string]string
	from   endpoint
	to     endpoint
	reason string
}

func normAmbiguousRows() []normAmbiguousRow {
	return []normAmbiguousRow{
		{"EVD-03 depends-on without via and repository", "DEPENDS_ON", map[string]string{"image": "x"}, epWorkload, epWorkload, "no rule matches"},
		{"depends-on with an unlisted via", "DEPENDS_ON", map[string]string{"via": "magic"}, epWorkload, epWorkload, "via=magic"},
		{"EVD-03 conflicting discriminators", "USAGE", map[string]string{"via": "terraform", "ref": "env"}, epTerraform, epSecret, "conflicting discriminators"},
		{"an unlisted usage ref is not ambiguous-usage", "USAGE", map[string]string{"ref": "projected"}, epWorkload, epSecret, "ref=projected"},
		{"usage ref env of an unlisted target kind", "USAGE", map[string]string{"ref": "env"}, epWorkload, epWorkload, "target kind Deployment"},
		{"usage ref env without a target kind", "USAGE", map[string]string{"ref": "env"}, epWorkload, endpoint{label: "Resource"}, "ref=env"},
		{"usage without ref from infrastructure", "USAGE", map[string]string{"callee": "x"}, epWorkload, epWorkload, "source infrastructure"},
		{"terraform usage with an unlisted ref", "USAGE", map[string]string{"via": "terraform", "ref": "data"}, epTerraform, epTerraform, "ref=data"},
		{"EVD-06 imports from a documentation file", "IMPORTS", map[string]string{"local_name": "x"}, epMarkdown, epGoFile, "source documentation"},
		{"EVD-06 imports without via from another yaml file", "IMPORTS", map[string]string{"local_name": "x"}, epYAMLFile, epYAMLFile, "source infrastructure"},
		{"A39 via=terraform from a code file", "IMPORTS", map[string]string{"via": "terraform"}, epGoFile, epGoFile, "via=terraform"},
		{"configures without a strategy", "CONFIGURES", nil, epWorkload, epWorkload, "no discriminator"},
		{"configures with an unlisted strategy", "CONFIGURES", map[string]string{"strategy": "other"}, epWorkload, epWorkload, "strategy=other"},
		{"infra-maps with an unlisted kind", "INFRA_MAPS", map[string]string{"kind": "gateway"}, epWorkload, epWorkload, "kind=gateway"},
		{"spawns with an unlisted api", "SPAWNS", map[string]string{"api": "exec"}, epWorkload, epWorkload, "api=exec"},
		{"supersedes without the markdown via", "SUPERSEDES", map[string]string{"syntax": "supersedes"}, epMarkdown, epMarkdown, "syntax=supersedes"},
	}
}

func TestAmbiguousFacts(t *testing.T) {
	for _, r := range normAmbiguousRows() {
		t.Run(r.name, func(t *testing.T) {
			e := normBetween(t, &normCase{native: r.native, props: r.props, from: r.from, to: r.to})

			got := [3]string{string(e.Kind), string(e.Resolution), e.NativeKind}
			if got != [3]string{string(observation.KindAmbiguous), string(observation.ResolutionAmbiguous), r.native} {
				t.Fatalf("edge = kind %s resolution %s native %s", e.Kind, e.Resolution, e.NativeKind)
			}

			if slices.Contains([]observation.CanonicalEdgeKind{observation.KindImports, observation.KindAmbiguousUsage}, e.Kind) {
				t.Fatalf("ambiguous fact has the kind %s", e.Kind)
			}

			if !strings.Contains(e.Facets["ambiguity"], r.reason) {
				t.Fatalf("ambiguity = %q, want it to contain %q", e.Facets["ambiguity"], r.reason)
			}

			normExpectProperties(t, e.Properties, r.props)
		})
	}
}

// normNativeOnlyKinds are the kinds that keep native evidence: no rule gives them.
var normNativeOnlyKinds = []observation.CanonicalEdgeKind{observation.KindUnsupported, observation.KindAmbiguous}

func normExpectRuleKind(t *testing.T, r *observation.NormalizationRule) {
	t.Helper()

	if !observation.IsCanonicalEdgeKind(string(r.Result.Kind)) {
		t.Errorf("rule %s gives the kind %q", r.Name, r.Result.Kind)
	}

	if slices.Contains(normNativeOnlyKinds, r.Result.Kind) {
		t.Errorf("rule %s gives the kind %q", r.Name, r.Result.Kind)
	}
}

func normExpectRuleRoleAndResolution(t *testing.T, r *observation.NormalizationRule) {
	t.Helper()

	if !observation.IsEvidenceRole(string(r.Result.Role)) {
		t.Errorf("rule %s has role %q resolution %q", r.Name, r.Result.Role, r.Result.Resolution)
	}

	if !observation.IsResolution(string(r.Result.Resolution)) {
		t.Errorf("rule %s has role %q resolution %q", r.Name, r.Result.Role, r.Result.Resolution)
	}
}

// Every rule gives a kind of the canonical list, has a unique name, and every
// native type of the table has rules; no rule gives a kind that only keeps
// native evidence.
func TestRuleTableIsConsistent(t *testing.T) {
	names := map[string]bool{}

	rules := observation.NormalizationRules()
	for i := range rules {
		r := &rules[i]

		if names[r.Name] || r.Name == "" {
			t.Errorf("rule name %q is empty or repeated", r.Name)
		}

		names[r.Name] = true

		normExpectRuleKind(t, r)
		normExpectRuleRoleAndResolution(t, r)

		if r.Result.Confidence == "" && !r.Result.UsesCallsRule {
			t.Errorf("rule %s has no confidence", r.Name)
		}
	}
}
