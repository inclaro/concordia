"""The check registry. Each check is `fn(ctx, check) -> (passed, detail)`."""

from __future__ import annotations

from .behaviour_checks import deterministic, review_report, reports_on_mutation, waived
from .result_checks import command, results, yaml_file
from .workspace_checks import (
    bound_nodes,
    branches_unchanged,
    calm_nodes,
    committed,
    model_valid,
    relationships,
    report_outputs,
    rules_declared,
    workflow,
)

CHECKS = {
    "committed": committed,
    "model_valid": model_valid,
    "calm_nodes": calm_nodes,
    "relationships": relationships,
    "bound_nodes": bound_nodes,
    "rules_declared": rules_declared,
    "results": results,
    "command": command,
    "yaml_file": yaml_file,
    "deterministic": deterministic,
    "review_report": review_report,
    "waived": waived,
    "workflow": workflow,
    "report_outputs": report_outputs,
    "reports_on_mutation": reports_on_mutation,
    "branches_unchanged": branches_unchanged,
}
