# ADR-0003: Database ownership

Status: Accepted

Date: 2026-01-15

Deciders: Team Gitea

## Context

Several packages open database sessions directly, which spreads transaction handling.

## Decision

Models, services and migrations own the database layer. Routers call services.

## Consequences

This decision is guidance for now and has no enforcing rule yet.
