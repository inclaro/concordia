package tui

import "github.com/junegunn/fzf/src/algo"

func benchAlgo() bool {
	return algo.Init("default")
}
