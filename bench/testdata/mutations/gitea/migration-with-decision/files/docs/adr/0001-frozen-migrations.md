# ADR-0001: Frozen migrations

Status: Accepted

Date: 2026-01-10

Deciders: Team Gitea

## Context

A migration that imports current application code changes whenever that code changes, so old migrations stop being reproducible.

## Decision

Code in `modelmigration` depends only on the database layer and on migration-safe shared modules. It never imports models, services or routers.

## Consequences

Migrations copy the struct definitions that they need. The rule GITEA-MIG-001 keeps migrations frozen.

## Addendum

New migration helpers in the base package follow the same rule.
