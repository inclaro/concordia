# ADR-0001: The ledger database is owned by the ledger service

Status: Accepted

Date: 2026-01-15

Deciders: Team Ledger, Team Architecture

## Context

Several services and jobs read the ledger tables directly. Each schema change of the ledger broke one of them, and a nightly report once read a half-written settlement.

## Decision

Only the ledger service reads and writes the ledger database. Other services and jobs use the ledger service API.

## Consequences

The rule PAY-DB-001 reports every access to the ledger database from outside the ledger service.
