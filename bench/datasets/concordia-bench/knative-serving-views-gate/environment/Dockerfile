FROM concordia-bench-knative-serving-views:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/webhook-leaves-namespace kn-placement-changed \
 && /opt/concordia-bench/bin/make-branch.sh pr/tweak-container-export kn-json-export-edited \
 && /opt/concordia-bench/bin/make-branch.sh pr/reconciler-uses-api-types controller-imports-apis
