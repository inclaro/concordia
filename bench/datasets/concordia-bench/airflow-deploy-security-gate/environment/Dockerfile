FROM concordia-bench-airflow-deploy-security:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/disable-scheduler-disruption-budget scheduler-pdb-removed \
 && /opt/concordia-bench/bin/make-branch.sh pr/sdk-reuses-own-definitions sdk-imports-sdk \
 && /opt/concordia-bench/bin/make-branch.sh pr/sdk-uses-core-models sdk-imports-models
