# Roadmap

## Purpose

This roadmap records directional priorities, not delivery promises. Ordering and
scope may change with implementation evidence, licensing constraints, operator
need, and upstream behavior. The [README](../README.md) is authoritative for
current operator behavior, and [ARCHITECTURE.md](ARCHITECTURE.md) is
authoritative for implemented boundaries and durable decisions.

When work is adopted, task-level scope, acceptance criteria, and progress should
live in GitHub issues or another explicitly approved public tracker. This file
should remain a concise statement of product direction.

## Current baseline: local v1

The implemented baseline compiles one locally acquired `GeoLite2-City` or
`GeoIP2-City` MMDB into a deterministic minimal artifact, proves exact lookup
equivalence, supports bounded inspection, and publishes a verified stable file
at `dist/artifacts/stategeodb.mmdb` on local macOS or Linux. It does not acquire
sources, apply corrections, compare or merge providers, produce JSON reports,
or deliver artifacts into Kubernetes.

## Priority 1: reviewed custom corrections

Add an opt-in declarative local correction file for reviewed geographic facts.
Each correction should identify a CIDR, country, optional subdivision, reason,
owner, and optional expiry. The design must provide:

- deterministic parsing, validation, and application;
- rejection of duplicate conflicting prefixes;
- longest-prefix precedence;
- application after provider normalization;
- correction provenance and counts in build evidence;
- strict separation between geographic facts and allow/deny policy.

Open design decisions include the file format and versioning rules, expiry
semantics, whether provenance belongs inside the artifact or only in external
evidence, and a safe preview workflow.

## Priority 2: independent verification and evidence

Add an operational `verify` command independent of build and publication. It
should recheck artifact checksum, source and output metadata, structure, and
runtime records; support bounded human-readable and JSON results; and allow
explicitly configured size, freshness, coverage, and change gates. It must not
expose a full licensed-data dump or weaken the publisher's independent
verification boundary.

## Priority 3: comparison and open-source research

Research legally usable free or open sources and treat comparison as an
evidence tool before considering any merge behavior. Review licensing,
freshness, update mechanisms, IPv4/IPv6 coverage, subdivision support,
attribution, redistribution constraints, and reproducible-fixture requirements.
Implement at most one adapter at a time, report missing values and
disagreements, and do not treat provider agreement as truth. No alternative
source is currently approved for production use, and comparison work must never
silently change published output.

## Priority 4: conservative fallback merging

If comparison evidence and licensing support it, consider an explicit opt-in
fallback policy with these constraints:

- the primary source remains authoritative by default;
- a secondary source may fill only missing values;
- primary conflicts remain primary and are reported, not silently resolved;
- a subdivision is accepted only with a compatible country;
- differing prefix boundaries are evaluated over their union;
- output and reports remain deterministic;
- accuracy, artifact size, and resource costs are measured before adoption;
- the feature remains opt-in until verified.

## Priority 5: Longhorn delivery

Define and test the contract for a separate trusted workflow that transfers a
verified stable artifact into a Longhorn-backed volume. Requirements include
source checksum confirmation, atomic replacement inside the PVC where the
filesystem supports it, permissions suitable for Traefik's runtime UID,
Traefik reload verification, retry and no-op behavior, failure preservation,
trusted checksum provenance, least-privilege workload identity and PVC access,
a single-writer transfer model, and cluster-specific topology and access-mode
handling. Compilation must not run in Traefik pods. Kubernetes manifests belong
in the owning cluster repository; this roadmap does not prescribe them.

## Later: resource optimization

Profile source-record storage, slice copies, lifetime overlap, and writer-owned
memory. Reduce only the contributors shown to dominate peak RSS, without
weakening determinism or complete equivalence verification. Consider streamed
or disk-backed equivalence only when measurements support it, and reassess the
writer itself only from evidence.

Any change needs before-and-after peak-RSS measurements on representative full
data, benchmark evidence for affected hot paths, IPv4 and IPv6 coverage, and
unchanged output bytes for a fixed input and build epoch. A 1 GiB limit is the
first target, 512 MiB is a stretch target, and 256 MiB is not a target for the
current architecture.

## Later: commercial source formats

Enterprise or other commercial formats remain low priority. Support requires a
concrete operator use case, permission to use the data, documented field
mapping, license-compatible fixtures, and measured artifact and resource
impact. Similar names or presumed schema compatibility are insufficient.

## Explicitly not planned now

- a request-time API service;
- a database network service;
- embedding allow/deny policy in geographic data;
- automatic data-source voting;
- direct cluster deployment from the CLI;
- publication of generated production databases in this public repository.
