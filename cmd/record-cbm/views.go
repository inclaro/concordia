package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/inclaro/concordia/internal/canonjson"
	"github.com/inclaro/concordia/internal/fsutil"
	"github.com/inclaro/concordia/internal/observation"
	"github.com/inclaro/concordia/internal/workspace"
)

const (
	// viewsRepoDirName is the directory (below the temporary directory) of the recorded view-currency repository.
	viewsRepoDirName = "concordia-record-viewcurrency"
	// viewsFixtureDir is relative to the module root.
	viewsFixtureDir = "test/fixtures/viewcurrency"
	// viewsProject is the CBM project name of the view-currency recordings.
	viewsProject = "concordia-fixture-viewcurrency"
)

// The files that the variants of the view-currency repository change or add (paths relative to the repository).
const (
	viewsArchitectureDoc = "docs/architecture.md"
	viewsOrdersFile      = "services/orders/src/orders.ts"
	viewsRefundFile      = "services/web/src/refund.ts"

	// viewsReorderedDoc is the architecture document with its two diagram blocks swapped. The flowchart of the
	// section `Shop containers` is then `#diagram-1` and not `#diagram-2` (O12).
	viewsReorderedDoc = "# Shop architecture\n\n## Shop containers\n\n" +
		"```mermaid\nflowchart LR\n" +
		"    customer([Customer]) --> web[Web App]\n" +
		"    web -->|HTTPS| orders[Orders API]\n" +
		"    orders -->|HTTPS| billing[Billing]\n" +
		"    orders -->|TCP| db[(Orders DB)]\n```\n\n" +
		"```mermaid\nsequenceDiagram\n    web->>orders: place order\n```\n"

	// viewsRefundSource is a web function that calls billing directly. The shop model has no relationship from
	// the web app to billing, so this is implementation drift (A16).
	viewsRefundSource = "import { charge } from \"../../billing/src/billing\";\n\n" +
		"export function refund(orderId: string, cents: number): string {\n" +
		"  return JSON.stringify(charge(orderId, -cents));\n}\n"

	// viewsRefactoredOrders is the orders service with one more helper and more calls between its functions. No
	// call between containers changes (A15).
	viewsRefactoredOrders = "import { charge, Charge } from \"../../billing/src/billing\";\n\n" +
		"function price(items: number[]): number {\n" +
		"  return items.reduce((sum, item) => sum + discount(item), 0);\n}\n\n" +
		"function discount(item: number): number {\n" +
		"  return item > 1000 ? item - 100 : item;\n}\n\n" +
		"function validate(items: number[]): boolean {\n" +
		"  return items.length > 0 && price(items) > 0;\n}\n\n" +
		"export function placeOrder(orderId: string, items: number[]): Charge | null {\n" +
		"  if (!validate(items)) return null;\n" +
		"  const total = price(items);\n" +
		"  return charge(orderId, total);\n}\n"
)

// viewsVariant is one recording of the view-currency repository.
type viewsVariant struct {
	// extra are files that replace or add files of the fixture before its commit.
	extra map[string]string
	// output is relative to the module root.
	output string
	// message is the commit message.
	message string
}

// viewsVariants are the recordings of the view-currency repository: as it is, with the diagram blocks in
// another order (A18), with a function that reaches billing from the web app (A16), and with an internal
// refactoring of the orders service (A15).
func viewsVariants() []viewsVariant {
	return []viewsVariant{
		{
			output:  "test/fixtures/cbm-recordings/viewcurrency.json",
			message: "view-currency fixture",
		},
		{
			output:  "test/fixtures/cbm-recordings/viewcurrency-reordered.json",
			message: "view-currency fixture with the diagram blocks swapped",
			extra:   map[string]string{viewsArchitectureDoc: viewsReorderedDoc},
		},
		{
			output:  "test/fixtures/cbm-recordings/viewcurrency-drift.json",
			message: "view-currency fixture with a web function that calls billing",
			extra:   map[string]string{viewsRefundFile: viewsRefundSource},
		},
		{
			output:  "test/fixtures/cbm-recordings/viewcurrency-refactored.json",
			message: "view-currency fixture with an internal refactoring of orders",
			extra:   map[string]string{viewsOrdersFile: viewsRefactoredOrders},
		},
	}
}

// recordViews records every variant of the view-currency repository.
func recordViews(ctx context.Context, root string) error {
	variants := viewsVariants()

	for i := range variants {
		if err := recordViewsVariant(ctx, root, &variants[i]); err != nil {
			return fmt.Errorf("%s: %w", variants[i].output, err)
		}
	}

	return nil
}

// recordViewsVariant indexes one variant with the real CBM, then runs the adapter and the whole-graph probe on
// it (recordSession), and the `index` and `check` commands of the workspace. The recording replays the queries
// of the replay tests and of the check pipeline.
func recordViewsVariant(ctx context.Context, root string, variant *viewsVariant) error {
	repoDir := filepath.Join(os.TempDir(), viewsRepoDirName)

	fixture := filepath.Join(root, filepath.FromSlash(viewsFixtureDir))
	if err := prepareRepo(ctx, fixture, repoDir, variant.message, variant.extra); err != nil {
		return err
	}

	defer func() { _ = os.RemoveAll(repoDir) }()

	headSHA, err := workspace.NewCliGit(repoDir).Revision(ctx)
	if err != nil {
		return fmt.Errorf("git revision: %w", err)
	}

	if headSHA == "" {
		return errors.New("no git revision after the commit")
	}

	raw := observation.NewProcessClient(observation.ProcessOptions{
		Executable:   cbmExecutable,
		Timeout:      cbmTimeout,
		IndexTimeout: cbmIndexTimeout,
		Retries:      cbmRetries,
	})

	defer closeClient(raw)

	// A project left by an earlier run must not change the first index call.
	deleteProject(ctx, raw, viewsProject)

	defer func() {
		// The cleanup runs after an interrupt too.
		deleteProject(context.WithoutCancel(ctx), raw, viewsProject)
	}()

	sink := observation.NewMemorySink(headSHA)
	client := observation.NewRecordingClient(raw, sink)

	version, err := client.Version(ctx)
	if err != nil {
		return fmt.Errorf("cbm version: %w", err)
	}

	if err := recordSession(ctx, client, &sessionRepo{dir: repoDir, project: viewsProject, headSHA: headSHA}, version); err != nil {
		return err
	}

	// The commands of record start their own CBM process: end this one first.
	closeClient(raw)

	if err := record(ctx, repoDir, sink); err != nil {
		return err
	}

	recording := sink.ToRecording()

	data, err := canonjson.Marshal(recording)
	if err != nil {
		return fmt.Errorf("encode the recording: %w", err)
	}

	output := filepath.Join(root, filepath.FromSlash(variant.output))
	if err = fsutil.WriteFileAtomic(output, data); err != nil {
		return fmt.Errorf("write the recording: %w", err)
	}

	_, _ = fmt.Fprintf(os.Stdout, "recorded %d call(s) at %s -> %s\n", len(recording.Calls), headSHA, output)

	return nil
}
