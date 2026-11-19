package benchpath

import "knative.dev/serving/pkg/reconciler/route"

func Hop() {
	route.NewController(nil, nil)
}
