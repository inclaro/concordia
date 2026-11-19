FROM concordia-bench-fzf-views:latest
RUN /opt/concordia-bench/bin/make-branch.sh pr/draw-protector-using-utilities diagram-drift \
 && /opt/concordia-bench/bin/make-branch.sh pr/renderer-uses-matcher model-changed \
 && /opt/concordia-bench/bin/make-branch.sh pr/tweak-generated-mermaid export-hand-edited \
 && /opt/concordia-bench/bin/make-branch.sh pr/tweak-generated-json json-export-edited \
 && /opt/concordia-bench/bin/make-branch.sh pr/tui-measures-text tui-imports-util
