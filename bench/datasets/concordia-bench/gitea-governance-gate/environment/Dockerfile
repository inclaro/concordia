FROM concordia-bench-gitea-governance:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/deprecate-layering-decision adr-deprecated \
 && /opt/concordia-bench/bin/make-branch.sh pr/retire-migration-decision adr-superseded-without-successor \
 && /opt/concordia-bench/bin/make-branch.sh pr/decisions-replace-each-other supersession-cycle \
 && /opt/concordia-bench/bin/make-branch.sh pr/unlink-migration-decision-rule decision-unlinked \
 && /opt/concordia-bench/bin/make-branch.sh pr/unlink-layering-decision-rule cov-layering-decision-unlinked \
 && /opt/concordia-bench/bin/make-branch.sh pr/downgrade-migration-rule rule-made-advisory \
 && /opt/concordia-bench/bin/make-branch.sh pr/migration-note-and-comment-fixes migration-with-decision baselined-files-touched
