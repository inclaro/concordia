package binding

import (
	"strconv"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/intent"
)

var extKinds = map[string]MatchKind{
	"source-root":    MatchSourceRoot,
	"package":        MatchPackage,
	"qualified-name": MatchQualifiedName,
	"build-target":   MatchBuildTarget,
	"otel-service":   MatchOtelService,

	"workload":        MatchWorkload,
	"image":           MatchImage,
	"route":           MatchRoute,
	"infra-resource":  MatchInfraResource,
	"compose-service": MatchComposeService,
}

// fromModel makes the bindings of the CALM `concordia.bindings` items. Their ID is `calm-ext:<node>:<i>`.
func fromModel(model *intent.ArchitectureModel, scp *scope, diagnostics *[]diag.Diagnostic) []Binding {
	var out []Binding

	for n := range model.Nodes {
		node := &model.Nodes[n]

		for i := range node.Bindings {
			site := matchSite{
				source:      model.SourceURI,
				pointer:     node.Pointer + "/concordia/bindings/" + strconv.Itoa(i),
				scp:         scp,
				diagnostics: diagnostics,
			}

			if binding, ok := modelBinding(node, i, site); ok {
				out = append(out, binding)
			}
		}
	}

	return out
}

// modelBinding makes the binding of the i-th `concordia.bindings` item of a
// node. A kind that is not supported adds a diagnostic and gives false.
func modelBinding(node *intent.NodeDeclaration, i int, site matchSite) (Binding, bool) {
	extBinding := &node.Bindings[i]

	kind, ok := extKinds[extBinding.Kind]
	if !ok {
		site.reject("binding kind '" + extBinding.Kind + "' is not supported")

		return Binding{}, false
	}

	matches := []Match{}
	if m, valid := modelMatch(kind, extBinding, site); valid {
		matches = append(matches, m)
	}

	return Binding{
		ID:              "calm-ext:" + node.ID + ":" + strconv.Itoa(i),
		ArchitectureRef: node.ID,
		Exclusive:       true,
		Required:        true,
		Cardinality:     orDefault(extBinding.Cardinality, CardinalityMany),
		Agreement:       orDefault(extBinding.Agreement, AgreementAll),
		Status:          orDefault(extBinding.Status, StatusActive),
		Authority:       extBinding.Authority,
		Match:           matches,
		Source:          site.source + "#" + site.pointer,
	}, true
}

// modelMatch makes the Match of a `concordia.bindings` item. A bad one adds a diagnostic and gives false.
func modelMatch(kind MatchKind, extBinding *intent.ExtBinding, site matchSite) (Match, bool) {
	if _, deployed := deployKinds[kind]; !deployed {
		return matchOf(string(kind), extBinding.Value, site)
	}

	return deployMatchOf(&Match{
		Kind: kind, Value: extBinding.Value, Environment: extBinding.Environment, Cluster: extBinding.Cluster,
		APIGroup: extBinding.APIGroup, ResourceKind: extBinding.ResourceKind, Namespace: extBinding.Namespace,
		Name: extBinding.Name, Repository: extBinding.Repository, Digest: extBinding.Digest,
		Address: extBinding.Address, File: extBinding.File, Service: extBinding.Service,
	}, site)
}
