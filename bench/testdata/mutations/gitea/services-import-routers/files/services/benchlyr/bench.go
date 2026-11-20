package benchlyr

import "gitea.dev/routers/common"

func benchRouter() {
	_ = common.BlockExpensive()
}
