// Command calm-parity records the results of `calm validate` (the CALM CLI)
// for the parity cases:
//
//	mise run calm:record
//
// It syncs the CALM schemas from the CALM CLI build, writes the upstream rule
// and release lists, imports the upstream test fixtures as cases, and writes
// the upstream outcome of each run to the expected.json of its case. The Go
// test in test/parity compares Concordia with these files without Node.js.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"

	"github.com/inclaro/concordia/internal/parity"
)

// schemasDir is the embedded CALM schema directory, relative to the module root.
const schemasDir = "internal/intent/schemas"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	err := run(ctx, os.Args[1:])

	stop()

	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "calm-parity: %v\n", err)

		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 || args[0] != "record" {
		return errors.New("usage: calm-parity record --upstream <dir> --node <file> --cases <dir>")
	}

	opts := parity.RecordOptions{Schemas: schemasDir, Log: os.Stdout}
	flags := flag.NewFlagSet("record", flag.ContinueOnError)
	flags.StringVar(&opts.Upstream, "upstream", "", "checkout with the built CALM CLI")
	flags.StringVar(&opts.Node, "node", "", "Node.js executable")
	flags.StringVar(&opts.Cases, "cases", "", "parity directory (with upstream.json and cases/)")

	if err := flags.Parse(args[1:]); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if opts.Upstream == "" || opts.Node == "" || opts.Cases == "" {
		return errors.New("record needs --upstream, --node, and --cases")
	}

	return parity.Record(ctx, &opts) //nolint:wrapcheck // the errors of Record name their cause.
}
