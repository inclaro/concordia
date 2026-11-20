FROM concordia-bench-strimzi-assurance:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/rename-unit-test-check typo-check-id \
 && /opt/concordia-bench/bin/make-branch.sh pr/user-operator-uses-api-model user-op-imports-api
