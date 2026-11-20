# ADR-0002: Layered architecture

Status: Superseded by [ADR-0001](0001-frozen-migrations.md)

Date: 2026-01-12

Deciders: Team Gitea

## Context

Handlers, business logic and data access were mixed, which produced import cycles.

## Decision

Code depends downward only: commands, routers, services, models, the database layer, then shared modules.

## Consequences

The rule GITEA-LYR-001 rejects a dependency from a lower layer to a higher one.
