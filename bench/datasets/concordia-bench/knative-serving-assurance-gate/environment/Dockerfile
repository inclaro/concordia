FROM concordia-bench-knative-serving-assurance:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/restore-activator-secret-access activator-secrets-widened \
 && /opt/concordia-bench/bin/make-branch.sh pr/scan-symlinked-resources render-symlink-source \
 && /opt/concordia-bench/bin/make-branch.sh pr/reconciler-uses-api-types controller-imports-apis
