package drf

import ctrlutil "volcano.sh/volcano/pkg/controllers/util"

func benchControllers() {
	ctrlutil.GetPodQuotaUsage(nil)
}
