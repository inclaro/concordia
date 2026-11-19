package queue

import "knative.dev/serving/pkg/autoscaler/metrics"

func benchAutoscaler() {
	metrics.ToWireStatMessages(nil)
}
