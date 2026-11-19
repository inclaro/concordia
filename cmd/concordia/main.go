// Command concordia checks architecture conformance: CALM intent versus the
// observed code graph.
package main

import (
	"context"
	"os"

	"github.com/inclaro/concordia/internal/cli"
)

var (
	version  = "dev"
	revision = "unknown"
	date     = "unknown"
)

func main() {
	os.Exit(cli.Main(context.Background(), os.Args[1:], os.Stdin, os.Stdout, os.Stderr, cli.BuildInfo{
		Version:  version,
		Revision: revision,
		Date:     date,
	}))
}
