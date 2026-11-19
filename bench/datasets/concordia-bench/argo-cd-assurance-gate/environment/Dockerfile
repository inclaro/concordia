FROM concordia-bench-argo-cd-assurance:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/open-redis-policy-in-overlay redis-netpol-opened \
 && /opt/concordia-bench/bin/make-branch.sh pr/overlay-adds-second-base cov-claim-render-broken \
 && /opt/concordia-bench/bin/make-branch.sh pr/server-uses-repo-server-client server-imports-reposerver-client
