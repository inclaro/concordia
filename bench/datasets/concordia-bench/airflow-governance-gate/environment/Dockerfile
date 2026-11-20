FROM concordia-bench-airflow-governance:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/drop-rule-link-from-decision decision-unlinked \
 && /opt/concordia-bench/bin/make-branch.sh pr/sdk-reuses-own-definitions sdk-imports-sdk
