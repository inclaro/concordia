package views_test

import (
	"bytes"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/inclaro/concordia/internal/deployment"
	"github.com/inclaro/concordia/internal/views"
)

func marshalProjection(t *testing.T, p *views.Projection) []byte {
	t.Helper()

	data, err := p.Marshal()
	if err != nil {
		t.Fatal(err)
	}

	return append(data, '\n')
}

func TestModelProjectionContainerView(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")
	got := views.BuildModelProjection(&view, &views.ModelInput{Model: projectionModel("HTTPS"), Bindings: projectionBindings()})

	if got.Contract != "concordia-view-projection/1" {
		t.Errorf("contract = %q", got.Contract)
	}

	ids := make([]string, len(got.Elements))
	for i, e := range got.Elements {
		ids[i] = e.ID
	}

	want := []string{"customer", "ledger", "orders-service", "payments-db", "audit-db", "web-ui"}
	slices.Sort(want)

	if !slices.Equal(ids, want) {
		t.Errorf("elements = %v, want %v", ids, want)
	}

	golden(t, "projection_container.json", marshalProjection(t, got))
}

func TestModelProjectionContextView(t *testing.T) {
	t.Parallel()

	view := contextView("payments-context")
	got := views.BuildModelProjection(&view, &views.ModelInput{Model: projectionModel("HTTPS"), Bindings: projectionBindings()})

	// The calls between the containers of the system are inside one element at this level: no self edge.
	for _, r := range got.Relationships {
		if r.From == r.To {
			t.Errorf("self edge %s -> %s in the context view", r.From, r.To)
		}
	}

	if len(got.Relationships) != 2 {
		t.Errorf("relationships = %+v, want the customer and the ledger relationship", got.Relationships)
	}

	golden(t, "projection_context.json", marshalProjection(t, got))
}

func TestModelProjectionDeploymentView(t *testing.T) {
	t.Parallel()

	view := deploymentView("payments-prod", "prod")
	placement := placementOf("prod", deployment.Placement{
		Node: "orders-service", Container: "cluster-a", Environment: "prod", Workload: "cbm:p:orders-1",
	})
	got := views.BuildModelProjection(&view, &views.ModelInput{Model: projectionModel("HTTPS"), Bindings: projectionBindings(), Placement: &placement})

	if got.PlacementProjection == nil || len(got.Entries) == 0 {
		t.Fatalf("a deployment view carries its placement: %+v", got.PlacementProjection)
	}

	golden(t, "projection_deployment.json", marshalProjection(t, got))
}

func TestModelProjectionDeploymentViewWithoutPlacementNamesItMissing(t *testing.T) {
	t.Parallel()

	view := deploymentView("payments-prod", "prod")
	got := views.BuildModelProjection(&view, &views.ModelInput{Model: projectionModel("HTTPS")})

	if !slices.Contains(got.Missing, views.MissingPlacement) {
		t.Errorf("missing = %v, want %q", got.Missing, views.MissingPlacement)
	}
}

func TestModelProjectionExplicitNullAndMissing(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")

	data, err := views.BuildModelProjection(&view, &views.ModelInput{Model: projectionModel("")}).Marshal()
	if err != nil {
		t.Fatal(err)
	}

	var doc struct {
		View          map[string]any   `json:"view"`
		Relationships []map[string]any `json:"relationships"`
		Missing       []string         `json:"missing"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}

	if v, ok := doc.View["environment"]; !ok || v != nil {
		t.Errorf("environment = %v (present %v), want an explicit null", v, ok)
	}

	for _, r := range doc.Relationships {
		for _, key := range []string{"protocol", "interface", "context", "status"} {
			if _, ok := r[key]; !ok {
				t.Errorf("relationship %v has no %q key: an absent value must be an explicit null", r, key)
			}
		}
	}

	if !slices.Contains(doc.Missing, views.MissingRelationshipInterface) || !slices.Contains(doc.Missing, views.MissingRelationshipContext) {
		t.Errorf("missing = %v, want the interface and context inputs named", doc.Missing)
	}
}

func TestModelProjectionIsSortedAndStable(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")
	in := &views.ModelInput{Model: projectionModel("HTTPS"), Bindings: projectionBindings()}

	first := marshalProjection(t, views.BuildModelProjection(&view, in))

	// The same architecture with the nodes and relationships declared in the other order.
	reversed := projectionModel("HTTPS")
	slices.Reverse(reversed.Nodes)
	slices.Reverse(reversed.Relationships)

	second := marshalProjection(t, views.BuildModelProjection(&view, &views.ModelInput{Model: reversed, Bindings: projectionBindings()}))

	if !bytes.Equal(first, second) {
		t.Errorf("declaration order changed the projection:\n%s\n%s", first, second)
	}
}

func TestModelProjectionParallelEdges(t *testing.T) {
	t.Parallel()

	model := projectionModel("HTTPS")
	model.Relationships = append(model.Relationships, connects("web-to-orders-async", "web-ui", "orders-service", "HTTPS"))

	merged := containerView("payments-container")
	separate := containerView("payments-container")
	separate.Projection = &views.ProjectionRules{ParallelEdges: "separate"}

	count := func(v *views.Definition) int {
		n := 0

		for _, r := range views.BuildModelProjection(v, &views.ModelInput{Model: model}).Relationships {
			if r.From == "web-ui" && r.To == "orders-service" {
				n++
			}
		}

		return n
	}

	if got := count(&merged); got != 1 {
		t.Errorf("merge: %d relationships, want the two declared ones merged by kind and protocol", got)
	}

	if got := count(&separate); got != 2 {
		t.Errorf("separate: %d relationships, want 2", got)
	}
}

func TestModelProjectionSemanticDisplayText(t *testing.T) {
	t.Parallel()

	plain := containerView("payments-container")
	named := containerView("payments-container")
	named.Projection = &views.ProjectionRules{Semantic: []string{views.SemanticName}}

	renamed := projectionModel("HTTPS")
	renamed.Nodes[3].Name = "Order Management"

	build := func(v *views.Definition, m *views.ModelInput) string {
		d, err := views.BuildModelProjection(v, m).Digest()
		if err != nil {
			t.Fatal(err)
		}

		return d
	}

	base := &views.ModelInput{Model: projectionModel("HTTPS")}
	changed := &views.ModelInput{Model: renamed}

	if build(&plain, base) != build(&plain, changed) {
		t.Error("a name change moved the projection of a view that does not declare names semantic")
	}

	if build(&named, base) == build(&named, changed) {
		t.Error("a name change did not move the projection of a view that declares names semantic")
	}
}

func TestModelProjectionCoverage(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")
	got := views.BuildModelProjection(&view, &views.ModelInput{Model: projectionModel("HTTPS")})

	wantElements := []string{"audit-db", "orders-service", "payments-db", "web-ui"}
	if !slices.Equal(got.Coverage.Required.Elements, wantElements) {
		t.Errorf("default necessary elements = %v, want %v", got.Coverage.Required.Elements, wantElements)
	}

	wantRelationships := []string{"orders-to-db", "web-to-orders"}
	if !slices.Equal(got.Coverage.Required.Relationships, wantRelationships) {
		t.Errorf("default necessary relationships = %v, want the relationships between the elements, %v", got.Coverage.Required.Relationships, wantRelationships)
	}

	view.Coverage = &views.Coverage{Elements: []string{"web-ui"}, Relationships: []string{"web-to-orders"}}
	declared := views.BuildModelProjection(&view, &views.ModelInput{Model: projectionModel("HTTPS")})

	if !slices.Equal(declared.Coverage.Required.Elements, []string{"web-ui"}) || !slices.Equal(declared.Coverage.Required.Relationships, []string{"web-to-orders"}) {
		t.Errorf("declared coverage = %+v", declared.Coverage.Required)
	}
}

func TestProjectionDigestIsSha256OfCanonicalJSON(t *testing.T) {
	t.Parallel()

	view := containerView("payments-container")
	p := views.BuildModelProjection(&view, &views.ModelInput{Model: projectionModel("HTTPS")})

	d, err := p.Digest()
	if err != nil {
		t.Fatal(err)
	}

	if !strings.HasPrefix(d, "sha256:") || len(d) != len("sha256:")+64 {
		t.Errorf("digest = %q", d)
	}
}
