package reposerver

import "github.com/argoproj/argo-cd/v3/applicationset/utils"

func benchCycle() {
	_ = utils.SecretsContainInClusterCredentials(nil)
}
