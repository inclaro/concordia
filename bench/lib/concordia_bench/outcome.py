"""Shared building blocks of the checks: result tuples and the mutation context."""

from __future__ import annotations

from contextlib import ExitStack, contextmanager

TAIL = 400


def result(problems: list[str], ok: str, summary: str = "") -> tuple[bool, str]:
    """(passed, detail) from a list of problems; `summary` is appended to a failure only."""
    if not problems:
        return True, ok
    return False, "; ".join(problems + ([summary] if summary else []))


def exit_result(done, ok: str) -> tuple[bool, str]:
    if done.exit == 0:
        return True, ok
    return False, f"exit {done.exit}: {(done.stderr or done.stdout).strip()[-TAIL:]}"


def exit_problems(code: int, want: int) -> list[str]:
    return [] if code == want else [f"check exit {code}, want {want}"]


@contextmanager
def mutated(ctx, name: str):
    """Apply a mutation for the duration of the block, then restore the repository."""
    before, files = ctx.apply_mutation(name)
    try:
        yield files
    finally:
        ctx.revert(before, files)


@contextmanager
def on_branch(ctx, branch: str | None, create: bool = False):
    """Check out `branch` (when given) for the duration of the block, then restore the previous one.

    With `create`, the branch is made from the current HEAD first and deleted afterwards, so a check
    can branch off state that only exists after the oracle ran (approvals, waivers on main)."""
    if not branch:
        yield
        return
    previous = ctx.checkout(branch, create=create)
    try:
        yield
    finally:
        ctx.checkout(previous)
        if create:
            ctx.delete_branch(branch)


@contextmanager
def prepared(ctx, check: dict):
    """Check out the check's `branch` (created from HEAD with `newBranch: true`), apply its
    `mutations` in order; undo both afterwards.

    Yields every file that the mutations created, overwrote or deleted."""
    with on_branch(ctx, check.get("branch"), bool(check.get("newBranch"))), ExitStack() as stack:
        changed: list[str] = []
        for name in check.get("mutations") or []:
            changed += stack.enter_context(mutated(ctx, name))
        yield changed
