FROM concordia-bench-volcano-review:latest
# The review task approves the assessment in its oracle run (not in its workspace): record the same approval on main here.
RUN rm -rf /tmp/cbm-daemon-* && bash -c 'set -euo pipefail; unset CI; cd /workspace/repo; concordia index; \
    out="$(concordia assessment approve ASM-ADMISSION --reviewer secops@bench.invalid 2>&1 || true)"; \
    digest="$(printf "%s\n" "$out" | grep -o "sha256:[0-9a-f]\{64\}" | tail -1)"; \
    concordia assessment approve ASM-ADMISSION --reviewer secops@bench.invalid --confirm-subject "$digest"; \
    git add -A; git add -f assessments; git commit -q -m "chore: approve ASM-ADMISSION"; concordia index' \
 && rm -rf /tmp/cbm-daemon-*
RUN /opt/concordia-bench/bin/make-branch.sh pr/track-admission-canary review-binding-widened \
 && /opt/concordia-bench/bin/make-branch.sh pr/stop-tracking-admission-init-job review-job-unbound \
 && /opt/concordia-bench/bin/make-branch.sh pr/add-security-reviewer review-reviewer-added \
 && /opt/concordia-bench/bin/make-branch.sh pr/detect-cgroup-driver-in-admission review-imports-agent
