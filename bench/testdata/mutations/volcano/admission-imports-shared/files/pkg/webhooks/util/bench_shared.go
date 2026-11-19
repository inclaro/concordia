package util

import commonutil "volcano.sh/volcano/pkg/util"

func benchShared() {
	commonutil.GenerateSchedulerName(nil)
}
