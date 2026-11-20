package tui

import "github.com/junegunn/fzf/src/util"

func benchInternal() {
	util.AtExit(func() {})
}
