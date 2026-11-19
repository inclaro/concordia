FROM concordia-bench-browser-use-core:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/classify-llm-adapters-top-secret bad-classification \
 && /opt/concordia-bench/bin/make-branch.sh pr/agent-reuses-shared-utilities agent-imports-utils
