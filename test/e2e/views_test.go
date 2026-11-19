package e2e_test

import (
	"strings"
	"testing"
)

const (
	viewsFixtureDir = "../fixtures/views"
	viewsProjectOld = "concordia-fixture-views"
	viewsProject    = "e2e-views"
	viewDSL         = "docs/architecture/views/payments-container.dsl"
	viewMermaid     = "docs/architecture/views/payments-container.mmd"
	viewManifest    = "docs/architecture/views/payments-container.dsl.manifest.json"
)

// The commands of docs/how-to/keep-diagrams-current.md on test/fixtures/views, against the real CBM and the real,
// pinned Structurizr CLI: validate, index, render --views, commit, check.
func TestViewsDocumentedCommands(t *testing.T) {
	requireE2E(t)

	repo := newRepo(t, repoSpec{source: viewsFixtureDir, oldName: viewsProjectOld, project: viewsProject, dirName: "views"})

	if res := repo.gu("model", "validate"); res.code != 0 {
		t.Fatalf("model validate (%d): %s", res.code, res.output())
	}

	if res := repo.gu("index"); res.code != 0 {
		t.Fatalf("index (%d): %s", res.code, res.stderr)
	}

	// Declare, render.
	res := repo.gu("render", "--views")
	if res.code != 0 {
		t.Fatalf("render --views (%d): %s", res.code, res.output())
	}

	for _, path := range []string{viewDSL, viewMermaid, viewManifest} {
		if !strings.Contains(res.stderr, "view written: "+path) {
			t.Errorf("render --views did not report %s: %s", path, res.stderr)
		}
	}

	dsl, mermaid, manifest := repo.read(viewDSL), repo.read(viewMermaid), repo.read(viewManifest)

	if !strings.Contains(dsl, `container gu_payments_system "payments-container"`) {
		t.Errorf("the Structurizr view is not named with the view ID:\n%s", dsl)
	}

	if !strings.Contains(mermaid, "Orders") {
		t.Errorf("the Mermaid export does not show the orders service:\n%s", mermaid)
	}

	for _, want := range []string{`"id": "payments-container"`, `"version": "2025.11.09"`, `"confinement": "scrubbed-env"`, `"schema": "concordia-view-manifest/1"`} {
		if !strings.Contains(manifest, want) {
			t.Errorf("the manifest lacks %s:\n%s", want, manifest)
		}
	}

	// Two runs on identical inputs write the same bytes.
	if res := repo.gu("render", "--views"); res.code != 0 {
		t.Fatalf("second render --views (%d): %s", res.code, res.output())
	}

	if repo.read(viewDSL) != dsl || repo.read(viewMermaid) != mermaid || repo.read(viewManifest) != manifest {
		t.Error("a second render changed a file")
	}

	// Commit, then check: check does not write a view file.
	repo.commitAll("docs: render the payments views")

	if res := repo.gu("index"); res.code != 0 {
		t.Fatalf("index after the commit (%d): %s", res.code, res.stderr)
	}

	if res := repo.gu("check"); res.code != 0 {
		t.Fatalf("check (%d): %s", res.code, res.output())
	}

	if repo.read(viewDSL) != dsl || repo.read(viewMermaid) != mermaid || repo.read(viewManifest) != manifest {
		t.Error("check changed a view file")
	}

	// Render again after the commit, limited to the view: the artifacts are the same bytes; only the revision in the
	// manifest is new.
	if res := repo.gu("render", "--views", "--view-id", "payments-container"); res.code != 0 {
		t.Fatalf("render --views --view-id (%d): %s", res.code, res.output())
	}

	if repo.read(viewDSL) != dsl || repo.read(viewMermaid) != mermaid {
		t.Error("a render after the commit changed an artifact")
	}

	if again := repo.read(viewManifest); differingLines(manifest, again) != 1 || !strings.Contains(again, `"revision"`) {
		t.Errorf("only the revision of the manifest should change after the commit:\n%s\n---\n%s", manifest, again)
	}

	// An output path that is not valid stops the command before it writes a file (exit code 2).
	repo.write(workspaceFile, strings.Replace(repo.read(workspaceFile), "path: docs/architecture/views/payments-container.dsl", "path: build/concordia/structurizr/views/payments-container.dsl", 1))

	bad := repo.gu("render", "--views")
	if bad.code != 2 || !strings.Contains(bad.stderr, "CO-DGM-002") {
		t.Errorf("an output path under projection.output: exit %d, stderr %s", bad.code, bad.stderr)
	}
}

// A view stays current while the architecture is the same, and goes stale when the architecture changes: render, commit,
// check passes; a CALM edit makes the freshness rule fail; a layout-only edit of a diagram leaves check passing.
func TestViewsFreshnessFollowsTheArchitectureNotTheLayout(t *testing.T) {
	requireE2E(t)

	const (
		calmFile   = "architecture/payments.calm.json"
		diagram    = "docs/arch/payments.md"
		freshness  = "VIEW-FRESH-001"
		layoutFrom = "flowchart LR"
		layoutTo   = "flowchart TB"
	)

	repo := newRepo(t, repoSpec{source: viewsFixtureDir, oldName: viewsProjectOld, project: viewsProject + "-fresh", dirName: "views-fresh"})

	if res := repo.gu("index"); res.code != 0 {
		t.Fatalf("index (%d): %s", res.code, res.stderr)
	}

	if res := repo.gu("render", "--views"); res.code != 0 {
		t.Fatalf("render --views (%d): %s", res.code, res.output())
	}

	repo.commitAll("docs: render the payments views")

	if res := repo.gu("index"); res.code != 0 {
		t.Fatalf("index after the commit (%d): %s", res.code, res.stderr)
	}

	freshnessFindings := func(doc *checkDoc) int {
		count := 0

		for _, f := range doc.Findings {
			if f.RuleID == freshness {
				count++
			}
		}

		return count
	}

	doc, res := repo.checkJSON(nil)
	if res.code != 0 || freshnessFindings(doc) != 0 {
		t.Fatalf("check after render and commit (%d, %d freshness findings): %s", res.code, freshnessFindings(doc), res.output())
	}

	// The architecture changes: the protocol of a relationship in the scope of the view.
	calm := repo.read(calmFile)
	if !strings.Contains(calm, `"protocol": "TCP"`) {
		t.Fatalf("the CALM of the fixture has no TCP protocol:\n%s", calm)
	}

	repo.write(calmFile, strings.Replace(calm, `"protocol": "TCP"`, `"protocol": "HTTPS"`, 1))

	doc, res = repo.checkJSON(nil)
	// The fixture view has severity warning, so the freshness findings are violations that do not set the exit code.
	if freshnessFindings(doc) == 0 {
		t.Fatalf("check after a CALM edit (%d): no %s finding: %s", res.code, freshness, res.output())
	}

	for _, f := range doc.Findings {
		if f.RuleID == freshness && f.Kind != "view-stale" {
			t.Errorf("freshness finding kind = %s, want view-stale", f.Kind)
		}
	}

	// Back to the committed architecture, then a layout-only edit of the authored diagram.
	repo.write(calmFile, calm)

	source := repo.read(diagram)
	if !strings.Contains(source, layoutFrom) {
		t.Fatalf("%s has no %q:\n%s", diagram, layoutFrom, source)
	}

	repo.write(diagram, strings.Replace(source, layoutFrom, layoutTo, 1))

	doc, res = repo.checkJSON(nil)
	if res.code != 0 || freshnessFindings(doc) != 0 {
		t.Fatalf("check after a layout-only edit (%d, %d freshness findings): %s", res.code, freshnessFindings(doc), res.output())
	}
}

// differingLines counts the lines of a that are not in b at the same place. The texts must have the same number of lines.
func differingLines(a, b string) int {
	left, right := strings.Split(a, "\n"), strings.Split(b, "\n")
	if len(left) != len(right) {
		return len(left) + len(right)
	}

	count := 0

	for i := range left {
		if left[i] != right[i] {
			count++
		}
	}

	return count
}
