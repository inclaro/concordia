package queue

import "knative.dev/serving/pkg/http"

func benchShared() {
	http.ExtractTraceID(nil)
}
