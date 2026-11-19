package route

import "knative.dev/serving/pkg/apis/serving"

func benchOK() {
	serving.VolumeMask(nil, nil)
}
