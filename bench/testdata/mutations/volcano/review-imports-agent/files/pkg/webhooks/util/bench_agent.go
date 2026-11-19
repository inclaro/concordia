package util

import "volcano.sh/volcano/pkg/agent/utils/cgroup"

func benchAgent() {
	cgroup.GetCgroupDriver()
}
