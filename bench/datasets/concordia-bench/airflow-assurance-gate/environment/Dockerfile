FROM concordia-bench-airflow-assurance:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/move-pgbouncer-port assurance-pgbouncer-port-moved \
 && /opt/concordia-bench/bin/make-branch.sh pr/sdk-reuses-own-definitions sdk-imports-sdk
