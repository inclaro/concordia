FROM concordia-bench-browser-use-review:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/move-llm-adapters-to-internet-zone llm-zone-changed \
 && /opt/concordia-bench/bin/make-branch.sh pr/drop-llm-trust-declaration llm-trust-dropped \
 && /opt/concordia-bench/bin/make-branch.sh pr/add-security-reviewer self-approval \
 && /opt/concordia-bench/bin/make-branch.sh pr/agent-reuses-tool-registry agent-imports-tools
