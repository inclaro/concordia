FROM concordia-bench-argo-cd-governance:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/deprecate-independence-decision adr-deprecated \
 && /opt/concordia-bench/bin/make-branch.sh pr/supersede-grpc-decision adr-superseded-without-successor \
 && /opt/concordia-bench/bin/make-branch.sh pr/drop-grpc-decision-enforcement decision-unlinked \
 && /opt/concordia-bench/bin/make-branch.sh pr/decisions-supersede-each-other supersession-cycle \
 && /opt/concordia-bench/bin/make-branch.sh pr/server-uses-repo-server-client server-imports-reposerver-client
