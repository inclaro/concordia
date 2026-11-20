FROM concordia-bench-volcano-deploy-security:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/drop-precedence-list precedence-removed \
 && /opt/concordia-bench/bin/make-branch.sh pr/use-shared-helpers-in-admission admission-imports-shared
