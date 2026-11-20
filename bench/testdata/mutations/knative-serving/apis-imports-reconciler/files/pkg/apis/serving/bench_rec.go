package serving

import "knative.dev/serving/pkg/reconciler/route"

func benchRec() {
	route.NewController(nil, nil)
}
