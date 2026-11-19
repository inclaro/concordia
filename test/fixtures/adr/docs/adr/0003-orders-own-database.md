# ADR-0003: Orders owns its database

Status: Accepted

Date: 2026-02-01

Deciders: Team Orders

## Context

Orders shares a schema with other services. Changes to the schema block releases.

## Decision

The orders service owns the `orders-db` database. No other service reads or writes it.

## Consequences

Other services call the orders service. The rule CORE-DB-001 keeps clients away from the database.
