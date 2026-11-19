package cli_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// exportTestVerifyDigestOnly tells whether the output of `export verify` shows the digest level, and no more.
func exportTestVerifyDigestOnly(code int, stdout string) bool {
	if code != 0 {
		return false
	}

	return strings.Contains(stdout, "content verified by digest") && !strings.Contains(stdout, "producer authenticated")
}

// exportTestVerifyRefusedNaming tells whether `export verify` stopped with exit 2 and a message with every text.
func exportTestVerifyRefusedNaming(code int, stderr string, texts ...string) bool {
	if code != 2 {
		return false
	}

	for _, text := range texts {
		if !strings.Contains(stderr, text) {
			return false
		}
	}

	return true
}

func exportTestVerifyCleanBundle(t *testing.T) {
	t.Helper()

	t.Parallel()

	s := newChkSetup(t, nil, nil)
	path := exportedBundle(t, s)

	res := s.run(t, "export", "verify", path)
	if !exportTestVerifyDigestOnly(res.Code, res.Stdout) {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}
}

func exportTestVerifyChangedFile(t *testing.T) {
	t.Helper()

	t.Parallel()

	s := newChkSetup(t, nil, nil)
	path := exportedBundle(t, s)
	tamper(t, path, func(n string, d []byte) ([]byte, bool) {
		if n == "results/results.json" {
			return append(d, ' '), true
		}

		return d, true
	}, nil)

	res := s.run(t, "export", "verify", path)
	if !exportTestVerifyRefusedNaming(res.Code, res.Stderr, "CO-REP-004", "results/results.json") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func exportTestVerifyMissingFile(t *testing.T) {
	t.Helper()

	t.Parallel()

	s := newChkSetup(t, nil, nil)
	path := exportedBundle(t, s)
	tamper(t, path, func(n string, d []byte) ([]byte, bool) { return d, n != "results/results.sarif" }, nil)

	res := s.run(t, "export", "verify", path)
	if !exportTestVerifyRefusedNaming(res.Code, res.Stderr, "results/results.sarif") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func exportTestVerifyExtraFile(t *testing.T) {
	t.Helper()

	t.Parallel()

	keep := func(_ string, d []byte) ([]byte, bool) { return d, true }

	s := newChkSetup(t, nil, nil)
	path := exportedBundle(t, s)
	tamper(t, path, keep, map[string]string{"extra.txt": "x"})

	res := s.run(t, "export", "verify", path)
	if !exportTestVerifyRefusedNaming(res.Code, res.Stderr, "extra.txt") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

func exportTestVerifySignature(t *testing.T) {
	t.Helper()

	t.Parallel()

	exportTestSkipWithoutShell(t)

	s := newChkSetup(t, nil, nil)
	path := exportedBundle(t, s)
	script := filepath.Join(t.TempDir(), "cosign")

	exportTestWriteScript(t, script, "#!/bin/sh\nexit 0\n")

	if err := os.WriteFile(path+".sigstore.json", []byte("sig"), 0o600); err != nil {
		t.Fatal(err)
	}

	s.cfg.Env["COSIGN"] = script

	res := s.run(t, "export", "verify", path, "--verify-signature", "--key", "k")
	if res.Code != 0 || !strings.Contains(res.Stdout, "producer authenticated") {
		t.Fatalf("exit code = %d, stdout = %q, stderr = %q", res.Code, res.Stdout, res.Stderr)
	}
}

func TestExportVerify(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		run  func(t *testing.T)
		name string
	}{
		{exportTestVerifyCleanBundle, "a clean bundle shows the digest level"},
		{exportTestVerifyChangedFile, "a changed file"},
		{exportTestVerifyMissingFile, "a missing file"},
		{exportTestVerifyExtraFile, "an extra file"},
		{exportTestVerifySignature, "a verified signature shows the authenticated level"},
	} {
		t.Run(tc.name, tc.run)
	}
}

// cosign 3 writes a signature made with a key to the public transparency log, so `export bundle --sign` stops with
// CO-WRK-016 (exit 6) under --offline, with or without --sign-key.
func TestExportBundleSigningOfflineIsExit6(t *testing.T) {
	// No cosign is installed: --offline is decided first, so the answer is CO-WRK-016 and not CO-WRK-014.
	t.Setenv("PATH", t.TempDir())

	s := newChkSetup(t, nil, nil)
	delete(s.cfg.Env, "COSIGN")

	res := s.run(t, "--offline", "export", "bundle", "--sign", "--sign-key", "k")
	if res.Code != 6 || !strings.Contains(res.Stderr, "CO-WRK-016") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}

// exportTestOfflineNeedsRoot tells whether the result is the CO-WRK-016 stop that asks for --trusted-root.
func exportTestOfflineNeedsRoot(code int, stderr string) bool {
	if code != 6 {
		return false
	}

	return strings.Contains(stderr, "CO-WRK-016") && strings.Contains(stderr, "--trusted-root")
}

// cosign 3 fetches the Sigstore trusted root to verify any signature. With --offline, `export verify
// --verify-signature` needs --trusted-root: without it the command stops with CO-WRK-016 (exit 6) before cosign runs;
// with it, cosign gets the file and verification runs offline.
func TestExportVerifySignatureOfflineNeedsATrustedRoot(t *testing.T) {
	t.Parallel()

	exportTestSkipWithoutShell(t)

	s := newChkSetup(t, nil, nil)
	path := exportedBundle(t, s)

	if err := os.WriteFile(path+".sigstore.json", []byte("sig"), 0o600); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	argsFile := filepath.Join(dir, "args.txt")
	script := filepath.Join(dir, "cosign")

	exportTestWriteScript(t, script, "#!/bin/sh\necho \"$@\" >> \""+argsFile+"\"\nexit 0\n")

	s.cfg.Env["COSIGN"] = script

	res := s.run(t, "--offline", "export", "verify", path, "--verify-signature", "--key", "k")
	if !exportTestOfflineNeedsRoot(res.Code, res.Stderr) {
		t.Fatalf("without --trusted-root: exit code = %d, stderr = %q", res.Code, res.Stderr)
	}

	if _, err := os.Stat(argsFile); err == nil {
		t.Fatal("cosign was started with --offline and no trusted root")
	}

	root := filepath.Join(dir, "trusted_root.json")
	res = s.run(t, "--offline", "export", "verify", path, "--verify-signature", "--key", "k", "--trusted-root", root)

	args, _ := os.ReadFile(argsFile)
	if res.Code != 0 || !strings.Contains(string(args), "--trusted-root "+root) {
		t.Fatalf("with --trusted-root: exit code = %d, stderr = %q, cosign arguments = %q", res.Code, res.Stderr, args)
	}
}

func TestExportVerifySignatureNeedsCosign(t *testing.T) {
	t.Setenv("PATH", t.TempDir())

	s := newChkSetup(t, nil, nil)
	path := exportedBundle(t, s)

	res := s.run(t, "export", "verify", path, "--verify-signature")
	if res.Code != 5 || !strings.Contains(res.Stderr, "CO-WRK-014") {
		t.Fatalf("exit code = %d, stderr = %q", res.Code, res.Stderr)
	}
}
