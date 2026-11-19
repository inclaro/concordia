# ADR-0002: The API server and the controllers are independent

Status: Deprecated

Date: 2026-02-01

Deciders: Team Argo CD

## Context

The API server, the application controller, the ApplicationSet controller and the notifications controller deploy and scale separately (see docs/proposals/003-applications-outside-argocd-namespace.md).

## Decision

None of the API server and the three controllers imports or calls another of them.

## Consequences

The decision is enforced by the architecture rules named in the decision register.
