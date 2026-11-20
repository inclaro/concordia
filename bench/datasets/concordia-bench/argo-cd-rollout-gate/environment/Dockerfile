FROM concordia-bench-argo-cd-rollout:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/make-security-rules-blocking mandatory-security \
 && /opt/concordia-bench/bin/make-branch.sh pr/pause-enforcement-until-2099 rollback-active \
 && /opt/concordia-bench/bin/make-branch.sh pr/pause-enforcement-until-2020 rollback-expired \
 && /opt/concordia-bench/bin/make-branch.sh pr/server-uses-repo-server-client server-imports-reposerver-client
