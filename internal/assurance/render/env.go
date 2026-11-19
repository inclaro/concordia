package render

import (
	"os"
	"path/filepath"
	"runtime"
)

// childEnv is the whole environment of a renderer process (design D4 layer 3). It is built up from a short list
// and does not copy the environment of Concordia, so proxies, `KUBECONFIG`, `HELM_*`, `XDG_*`, cloud credentials,
// and tokens cannot reach the renderer. HOME and the cache, config, data, and temporary directories point into
// home, a directory that Concordia creates and removes.
func childEnv(home string) []string {
	env := []string{
		"HOME=" + home,
		"TMPDIR=" + filepath.Join(home, "tmp"),
		"XDG_CACHE_HOME=" + filepath.Join(home, "cache"),
		"XDG_CONFIG_HOME=" + filepath.Join(home, "config"),
		"XDG_DATA_HOME=" + filepath.Join(home, "data"),
		"HELM_CACHE_HOME=" + filepath.Join(home, "cache", "helm"),
		"HELM_CONFIG_HOME=" + filepath.Join(home, "config", "helm"),
		"HELM_DATA_HOME=" + filepath.Join(home, "data", "helm"),
		"LC_ALL=C",
	}

	if runtime.GOOS == "windows" {
		// Windows programs need the system root to start; nothing else is passed.
		env = append(env, "SystemRoot="+os.Getenv("SystemRoot"), "TEMP="+filepath.Join(home, "tmp"), "TMP="+filepath.Join(home, "tmp"))
	} else {
		env = append(env, "PATH=/usr/bin:/bin")
	}

	return env
}
