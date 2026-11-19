package e2e_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
)

const (
	e2eEnvName = "CONCORDIA_E2E"
	buildDir   = "../.."
	binaryName = "concordia"
)

// concordiaBin is the path of the binary that the tests drive. TestMain builds it once.
var concordiaBin string

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	if os.Getenv(e2eEnvName) != "1" {
		// Every test skips. Do not build the binary.
		return m.Run()
	}

	dir, err := os.MkdirTemp("", "concordia-e2e-bin-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create build directory: %v\n", err)

		return 1
	}

	defer func() { _ = os.RemoveAll(dir) }()

	name := binaryName
	if runtime.GOOS == "windows" {
		name += ".exe"
	}

	concordiaBin = filepath.Join(dir, name)

	build := exec.CommandContext(context.Background(), "go", "build", "-o", concordiaBin, "./cmd/concordia")
	build.Dir = buildDir
	build.Stdout = os.Stdout
	build.Stderr = os.Stderr

	if err := build.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "go build ./cmd/concordia: %v\n", err)

		return 1
	}

	return m.Run()
}
