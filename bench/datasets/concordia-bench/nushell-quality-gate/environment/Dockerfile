FROM concordia-bench-nushell-quality:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/require-security-quality-area security-area-required \
 && /opt/concordia-bench/bin/make-branch.sh pr/rename-unit-tests-check typo-check-id \
 && /opt/concordia-bench/bin/make-branch.sh pr/use-protocol-in-commands command-uses-protocol
