package server

import repoapiclient "github.com/argoproj/argo-cd/v3/reposerver/apiclient"

func benchRepoClient() {
	repoapiclient.ResetClientCertCache()
}
