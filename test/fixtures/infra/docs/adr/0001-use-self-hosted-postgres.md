# ADR-0001: Use self-hosted Postgres

Status: Superseded by [ADR-0002](0002-use-managed-postgres.md)

Date: 2026-01-10

## Context

The payments API needs a relational store. The team runs Postgres in Compose.

## Decision

We run Postgres ourselves.

## Consequences

We own backups and upgrades.
