FROM concordia-bench-strimzi-rollout:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/make-placement-mandatory mandatory-placement \
 && /opt/concordia-bench/bin/make-branch.sh pr/let-rollback-lapse rollback-expired \
 && /opt/concordia-bench/bin/make-branch.sh pr/end-rollback rollback-removed \
 && /opt/concordia-bench/bin/make-branch.sh pr/user-operator-uses-api-model user-op-imports-api
