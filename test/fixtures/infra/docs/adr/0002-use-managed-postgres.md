# ADR-0002: Use managed Postgres

Status: Accepted

Date: 2026-03-02

Supersedes [ADR-0001](0001-use-self-hosted-postgres.md).

## Context

Operating Postgres ourselves costs too much on-call time. The Terraform root
module in `terraform/main.tf` declares the managed instance.

## Decision

We use a managed Postgres instance.

## Consequences

Backups and upgrades move to the provider.
