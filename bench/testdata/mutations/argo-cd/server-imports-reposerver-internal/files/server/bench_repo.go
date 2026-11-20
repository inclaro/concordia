package server

import "github.com/argoproj/argo-cd/v3/reposerver/repository"

func benchRepoInternal() {
	_ = repository.NewManifestResponsePromise(nil, nil, nil)
}
