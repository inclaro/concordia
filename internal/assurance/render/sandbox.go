package render

import (
	"context"
	"runtime"

	"github.com/inclaro/concordia/internal/diag"
	"github.com/inclaro/concordia/internal/procexec"
)

// sandboxSpec is what the OS sandbox of one render needs to know.
type sandboxSpec struct {
	// Bwrap is the path of the bubblewrap executable.
	Bwrap string
	// Executable is the renderer, bound read-only.
	Executable string
	// In is the confined copy of the declared inputs, bound read-only. It is also the working directory.
	In string
	// Home is the HOME and cache directory of the renderer, the only writable bind.
	Home string
}

// systemBinds are the host directories that a dynamically linked program needs. They are bound read-only and only
// when they exist. /etc, the home of the user, and /tmp are not bound: the renderer sees no configuration and no
// credentials of the host.
var systemBinds = []string{usrDir, binDir, "/sbin", libDir, lib64Dir}

const (
	binDir    = "/bin"
	usrDir    = "/usr"
	libDir    = "/lib"
	lib64Dir  = "/lib64"
	roBindTry = "--ro-bind-try"
)

// bwrapArgs builds the bubblewrap arguments that run command in the sandbox (design D4 layer 7): a new network
// namespace without interfaces, new PID, IPC, and UTS namespaces, an empty root with read-only binds of the system
// directories, the renderer, and the input copy, one writable bind for HOME, and the working directory inside the
// copy. The environment of the command is set by procexec and passes through bubblewrap unchanged.
func bwrapArgs(spec sandboxSpec, command []string) []string {
	args := []string{
		"--die-with-parent", "--new-session",
		"--unshare-net", "--unshare-pid", "--unshare-ipc", "--unshare-uts",
	}

	for _, dir := range systemBinds {
		args = append(args, roBindTry, dir, dir)
	}

	args = append(args,
		"--proc", "/proc", "--dev", "/dev",
		"--ro-bind", spec.Executable, spec.Executable,
		"--ro-bind", spec.In, spec.In,
		"--bind", spec.Home, spec.Home,
		"--chdir", spec.In,
		"--",
	)

	return append(args, command...)
}

// findSandbox says whether the OS sandbox can be used: Linux, bubblewrap found, and a trial run with the same
// namespaces succeeds (user namespaces may be disabled). It gives the bubblewrap path.
func findSandbox(ctx context.Context, run Runner, locate Locator) (string, bool) {
	if runtime.GOOS != "linux" {
		return "", false
	}

	bwrap, err := locate("bwrap")
	if err != nil {
		return "", false
	}

	res, err := run(ctx, bwrap, []string{
		"--unshare-net", "--unshare-pid", roBindTry, usrDir, usrDir, roBindTry, binDir, binDir,
		roBindTry, libDir, libDir, roBindTry, lib64Dir, lib64Dir, "--proc", "/proc", "--dev", "/dev", "--",
		binDir + "/sh", "-c", "exit 0",
	}, procexec.Options{Timeout: probeTimeout, MaxBuffer: probeMaxBuffer, Env: []string{"PATH=/usr/bin:" + binDir}})
	if err != nil || res.Code != 0 || res.TimedOut {
		return "", false
	}

	return bwrap, true
}

// requireSandbox gives the CO-PRV-004 error (exit 5) for `requireOsSandbox` when no sandbox can be used.
func requireSandbox(found bool) error {
	if found {
		return nil
	}

	return diag.Fail(diag.PRV004, diag.Detail("assurance.rendering.requireOsSandbox is set and no OS sandbox is available (Linux with bubblewrap and user namespaces is needed)"))
}
