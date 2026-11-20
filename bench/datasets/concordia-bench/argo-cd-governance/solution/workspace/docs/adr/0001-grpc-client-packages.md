# ADR-0001: Components reach the gRPC services through their client packages

Status: Approved

Date: 2026-02-01

Deciders: Team Argo CD

## Context

The repo server, the commit server and the config management plugin server are separate processes (see docs/proposals/config-management-plugin-v2.md and docs/proposals/manifest-hydrator.md). Components that import their implementation packages cannot be released apart.

## Decision

Components use the repo server, the commit server and the plugin server only through their apiclient packages.

## Consequences

The decision is enforced by the architecture rules named in the decision register.
