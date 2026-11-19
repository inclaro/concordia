package serving

import "knative.dev/serving/pkg/webhook"

func benchWh() {
	webhook.ValidateService(nil, nil)
}
