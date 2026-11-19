package controller

import "github.com/argoproj/argo-cd/v3/server/cluster"

func benchServerPeer() string {
	return cluster.CreateClusterRBACObject("bench", "bench")
}
