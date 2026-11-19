package user

import "gitea.dev/routers/common"

func benchDomain() {
	_ = common.BlockExpensive()
}
