# ADR-0007: Orders uses a managed database

Status: Accepted

Date: 2026-04-15

Deciders: Team Orders

Supersedes [ADR-0003](0003-orders-own-database.md).

## Context

Operating the database ourselves costs on-call time. The code in `services/orders/src/orders.ts` only needs a connection.

## Decision

The orders service keeps owning its data and runs it on a managed instance.

## Consequences

Backups and upgrades move to the provider.
