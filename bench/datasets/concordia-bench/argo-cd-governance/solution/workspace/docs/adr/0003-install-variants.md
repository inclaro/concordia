# ADR-0003: Argo CD ships namespace-scoped and HA install variants

Status: Accepted

Date: 2026-02-01

Deciders: Team Argo CD

## Context

Operators install Argo CD from generated manifests in the manifests directory: a namespace-scoped install and a high-availability install.

## Decision

The install manifests are generated and committed; a change to them is an architecture decision.

## Consequences

The decision is enforced by the architecture rules named in the decision register.
