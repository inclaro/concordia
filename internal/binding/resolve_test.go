package binding_test

import (
	"reflect"
	"slices"
	"testing"

	"github.com/inclaro/concordia/internal/binding"
	"github.com/inclaro/concordia/internal/observation"
)

func node(id, qn, filePath string) observation.ObservedNode {
	return observation.ObservedNode{ObservedID: "cbm:p:" + id, Label: "Function", QualifiedName: qn, FilePath: filePath}
}

func bind(id, ref string, kind binding.MatchKind, value string, exclusive ...bool) binding.Binding {
	excl := true
	if len(exclusive) > 0 {
		excl = exclusive[0]
	}

	return binding.Binding{
		ID: id, ArchitectureRef: ref, Exclusive: excl, Required: true,
		Match: []binding.Match{{Kind: kind, Value: value}}, Source: "t",
	}
}

var invoice = node("1", "services.billing.src.Invoice", "services/billing/src/Invoice.ts")

func TestALongerSourceRootWinsAgainstAShorterOne(t *testing.T) {
	t.Parallel()

	set := binding.Resolve([]binding.Binding{
		bind("a", "outer", binding.MatchSourceRoot, "services/**"),
		bind("b", "inner", binding.MatchSourceRoot, "services/billing/**"),
	}, []observation.ObservedNode{invoice}, nil)

	if len(set.Conflicts) != 0 {
		t.Errorf("conflicts = %+v", set.Conflicts)
	}

	if got := set.ByObserved["cbm:p:1"]; !slices.Equal(got, []string{"inner"}) {
		t.Errorf("bound = %v", got)
	}

	if set.MatchCounts["a"] != 1 {
		t.Errorf("match count a = %d", set.MatchCounts["a"])
	}
}

func TestQualifiedNameBeatsPackageBeatsSourceRoot(t *testing.T) {
	t.Parallel()

	set := binding.Resolve([]binding.Binding{
		bind("a", "root", binding.MatchSourceRoot, "services/billing/src/**"),
		bind("p", "pkg", binding.MatchPackage, "services.billing.."),
		bind("q", "qn", binding.MatchQualifiedName, "services.billing.src.Invoice"),
	}, []observation.ObservedNode{invoice}, nil)

	if got := set.ByObserved["cbm:p:1"]; !slices.Equal(got, []string{"qn"}) {
		t.Errorf("bound = %v", got)
	}
}

func TestTwoExclusiveBindingsWithEqualSpecificityConflict(t *testing.T) {
	t.Parallel()

	set := binding.Resolve([]binding.Binding{
		bind("a", "x", binding.MatchSourceRoot, "services/billing/**"),
		bind("b", "y", binding.MatchSourceRoot, "services/billing/**"),
	}, []observation.ObservedNode{invoice}, nil)

	want := []binding.Conflict{{ObservedID: "cbm:p:1", CalmIDs: []string{"x", "y"}, BindingIDs: []string{"a", "b"}}}
	if !reflect.DeepEqual(set.Conflicts, want) || len(set.ByObserved) != 0 {
		t.Fatalf("set = %+v", set)
	}
}

func TestTwoNonExclusiveBindingsBindTheNodeToBothCalmIDs(t *testing.T) {
	t.Parallel()

	set := binding.Resolve([]binding.Binding{
		bind("a", "x", binding.MatchSourceRoot, "services/billing/**", false),
		bind("b", "y", binding.MatchSourceRoot, "services/billing/**", false),
	}, []observation.ObservedNode{invoice}, nil)

	if len(set.Conflicts) != 0 || !slices.Equal(set.ByObserved["cbm:p:1"], []string{"x", "y"}) {
		t.Fatalf("set = %+v", set)
	}
}

func TestResolveDoesNotDependOnBindingOrderAndCountsUnboundNodes(t *testing.T) {
	t.Parallel()

	bs := []binding.Binding{
		bind("a", "x", binding.MatchPackage, "services.billing.."),
		bind("b", "y", binding.MatchPackage, "other.."),
	}
	nodes := []observation.ObservedNode{invoice, node("2", "misc.thing", "misc/thing.ts")}

	one := binding.Resolve(bs, nodes, nil)
	two := binding.Resolve([]binding.Binding{bs[1], bs[0]}, []observation.ObservedNode{nodes[1], nodes[0]}, nil)

	if !reflect.DeepEqual(one.ByObserved, two.ByObserved) {
		t.Errorf("by observed differs: %v / %v", one.ByObserved, two.ByObserved)
	}

	if one.UnboundObserved != 1 || one.MatchCounts["b"] != 0 {
		t.Errorf("set = %+v", one)
	}
}

func TestSourceRootGlobMatchesDotFilesAndSkipsNodesWithoutAFile(t *testing.T) {
	t.Parallel()

	set := binding.Resolve(
		[]binding.Binding{bind("a", "x", binding.MatchSourceRoot, "services/**")},
		[]observation.ObservedNode{node("1", "a", "services/.hidden/x.ts"), node("2", "b", "")},
		nil,
	)

	if !slices.Equal(set.ByObserved["cbm:p:1"], []string{"x"}) || set.UnboundObserved != 1 {
		t.Fatalf("set = %+v", set)
	}
}

func TestBuildTargetMatchesTheFilesOfABuildPackage(t *testing.T) {
	t.Parallel()

	nodes := []observation.ObservedNode{
		node("1", "a.x", "a/x.go"), node("2", "a.b.y", "a/b/y.go"), node("3", "c.z", "c/z.go"),
	}
	buildFiles := binding.BuildFiles([]string{"a/BUILD", "a/b/BUILD", "a/x.go", "c/BUCK.txt"})

	if !slices.Equal(buildFiles, []string{"a/BUILD", "a/b/BUILD"}) {
		t.Fatalf("build files = %v", buildFiles)
	}

	cases := map[string]struct {
		label string
		want  []string
	}{
		"package":        {"//a", []string{"cbm:p:1"}},
		"package:target": {"//a:lib", []string{"cbm:p:1"}},
		"recursive":      {"//a/...", []string{"cbm:p:1", "cbm:p:2"}},
		"all":            {"//...", []string{"cbm:p:1", "cbm:p:2"}},
	}

	for name, tc := range cases {
		set := binding.Resolve([]binding.Binding{bind("a", "x", binding.MatchBuildTarget, tc.label)}, nodes, buildFiles)

		got := make([]string, 0, len(set.ByObserved))
		for id := range set.ByObserved {
			got = append(got, id)
		}

		slices.Sort(got)

		if !slices.Equal(got, tc.want) {
			t.Errorf("%s: bound = %v, want %v", name, got, tc.want)
		}
	}
}

func TestOtelServiceBindsOnlyTheServiceNode(t *testing.T) {
	t.Parallel()

	svc := observation.ObservedNode{ObservedID: observation.OtelServicePrefix + "billing", Label: "Service", QualifiedName: "billing"}
	fn := node("1", "billing", "")

	set := binding.Resolve([]binding.Binding{bind("o", "x", binding.MatchOtelService, "billing")}, []observation.ObservedNode{svc, fn}, nil)
	if len(set.ByObserved) != 1 || !slices.Equal(set.ByObserved[svc.ObservedID], []string{"x"}) {
		t.Errorf("otel binding: %+v", set.ByObserved)
	}

	set = binding.Resolve([]binding.Binding{bind("q", "y", binding.MatchQualifiedName, "billing")}, []observation.ObservedNode{svc, fn}, nil)
	if _, bound := set.ByObserved[svc.ObservedID]; bound || !slices.Equal(set.ByObserved[fn.ObservedID], []string{"y"}) {
		t.Errorf("qualified-name binding: %+v", set.ByObserved)
	}
}

func TestLiteralPrefixLength(t *testing.T) {
	t.Parallel()

	for glob, want := range map[string]int{"services/billing/**": 17, "a?b": 1, "plain": 5, "x{a,b}": 1, "[a]": 0} {
		if got := binding.LiteralPrefixLength(glob); got != want {
			t.Errorf("LiteralPrefixLength(%q) = %d, want %d", glob, got, want)
		}
	}
}

func TestWithFileNodesAddsSyntheticFileNodesSortedByID(t *testing.T) {
	t.Parallel()

	nodes := map[string]observation.ObservedNode{
		"cbm:p:2": {ObservedID: "cbm:p:2", Label: "File", FilePath: "a.ts"},
		"cbm:p:1": {ObservedID: "cbm:p:1", Label: "Function", FilePath: "b.ts", QualifiedName: "b.f"},
	}

	got := binding.WithFileNodes("p", nodes, []string{"a.ts", "b.ts"})

	ids := make([]string, 0, 8)
	for i := range got {
		ids = append(ids, got[i].ObservedID)
	}

	if want := []string{"cbm:p:1", "cbm:p:2", "cbm:p:file:b.ts"}; !slices.Equal(ids, want) {
		t.Fatalf("ids = %v", ids)
	}
}

func TestBuildTargetMatchesFilesOfASecondaryProject(t *testing.T) {
	t.Parallel()

	nodes := []observation.ObservedNode{
		node("1", "a.x", "a/x.go"), node("2", "p.api", "@pay/api/h.go"), node("3", "p.root", "@pay/r.go"), node("4", "p.deep", "@pay/api/v1/d.go"),
	}
	buildFiles := binding.BuildFiles([]string{"a/BUILD", "@pay/BUILD", "@pay/api/BUILD", "@pay/api/v1/BUILD"})

	cases := map[string][]string{
		"//@pay/api":     {"cbm:p:2"},
		"//@pay/api/...": {"cbm:p:2", "cbm:p:4"},
		"//@pay":         {"cbm:p:3"},
		"//@pay/...":     {"cbm:p:2", "cbm:p:3", "cbm:p:4"},
		"//a":            {"cbm:p:1"},
	}

	for label, want := range cases {
		set := binding.Resolve([]binding.Binding{bind("a", "x", binding.MatchBuildTarget, label)}, nodes, buildFiles)

		got := make([]string, 0, len(set.ByObserved))
		for id := range set.ByObserved {
			got = append(got, id)
		}

		slices.Sort(got)

		if !slices.Equal(got, want) {
			t.Errorf("%s: bound = %v, want %v", label, got, want)
		}
	}
}
