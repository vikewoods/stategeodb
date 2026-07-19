# Repository guidance

## Mission

Build `stategeodb` as a deterministic, automation-first Go CLI that compiles
licensed geolocation sources into a minimal MMDB for local use by
`traefik-plugin-state-geo`.

The CLI is an offline build tool. It is not a request-time service and must not
introduce a network, SQL, document-database, cache-server, or CDN dependency
into Traefik's request path.

## Read before changing code

1. Read `README.md` for project boundaries and the planned command surface.
2. Read `docs/ARCHITECTURE.md` for component ownership and durable decisions.
3. When present, read the active phase in `docs/PHASES.md` and only its matching
   prompt under `docs/phases/`. Both paths are intentionally ignored by Git.
4. Inspect the current code and tests before selecting packages or changing an
   interface.

If a phase file conflicts with committed architecture or a newer user request,
follow the newer user request and update the local plan rather than silently
diverging.

## Authorization boundaries

- For review, explanation, diagnosis, or planning requests, inspect relevant
  files and report findings. Do not implement changes unless requested.
- For build, change, or fix requests, make the requested in-repository edits and
  run relevant non-destructive validation without pausing for routine approval.
- Require explicit confirmation before publishing images or releases, pushing
  Git changes, deploying to a cluster, writing to another repository, deleting
  material data, rotating credentials, or materially expanding the phase.
- Never download or redistribute licensed production data unless the task
  explicitly authorizes that source and use.

## Project boundaries

- Keep the compiler in this repository and the Traefik middleware in its own
  repository. Do not copy writer or CLI dependencies into the Yaegi plugin.
- Keep live Kubernetes resources in their cluster repositories. This repository
  may contain reviewed reference manifests, schemas, and examples only.
- Keep geographic data separate from access-control policy. Location
  corrections may change country or subdivision facts; allow/deny rules belong
  to middleware or cluster policy.
- Keep generated databases, credentials, source archives, build workspaces, and
  private phase documents out of Git.

## Go and CLI conventions

- Use the Go version declared by `go.mod` once the module is initialized.
- Keep `main` minimal. Command handlers parse input and delegate to internal
  packages; domain logic must remain independently testable.
- Prefer `net/netip` for IPv4, IPv6, prefixes, and address normalization.
- Pass `context.Context` through acquisition, compilation, verification, and
  publication operations that can block or be cancelled.
- Use explicit constructors and narrow interfaces at source, compiler, writer,
  verifier, and publisher boundaries.
- Prefer the standard library. Add a production dependency only when it has a
  clear ownership, maintenance, security, or correctness advantage.
- Use the upstream MMDB reader and MaxMind writer only in this compiled tool;
  do not modify or reuse the Traefik plugin's Yaegi compatibility shim.
- Avoid package globals for mutable build state. A command invocation must own
  its configuration, inputs, temporary files, and result.

### Command behavior

- Commands must be non-interactive and safe for CronJobs and shell pipelines.
- Machine-readable results go to stdout. Logs and diagnostics go to stderr.
- Add explicit JSON output when report-producing or inspection commands have
  real structured results; current help and version output remains text-only.
- Invalid flags or configuration are usage failures; corrupt inputs,
  verification failures, and publication failures are distinguishable through
  diagnostics and, when domain behavior exists, internal error classification.
- The current process-status contract is binary: `0` for success and `1` for
  failure. Change it only for a concrete automation requirement.
- Do not call `os.Exit` below `main`; return errors so deferred cleanup runs.
- Do not print credentials, authenticated URLs, source archives, or client IP
  samples in normal logs.
- Blocking operations must honor caller cancellation when introduced, and
  cancellation must leave the current published MMDB unchanged.

## Data and merge invariants

- IPv4 and IPv6 behavior must be covered by the same feature and test work.
- Normalize output to `country.iso_code` and optional
  `subdivisions[0].iso_code` unless architecture is deliberately revised.
- Identical inputs, configuration, overrides, and injected build time must
  produce byte-identical output.
- Primary values win conflicts by default. A secondary source may fill missing
  data only according to the configured merge policy.
- Provider disagreement is evidence of uncertainty, not proof that either
  provider is correct.
- Longest prefix wins for location overrides. Duplicate conflicting prefixes
  are build errors.
- Apply overrides after provider merging and record their provenance in the
  build report.
- Never replace the published artifact until the complete candidate passes all
  configured structural and behavioral gates.

## Filesystem and publication safety

- Create candidates in temporary paths on the same filesystem as the final
  destination when an atomic rename is required.
- Reopen and verify the completed candidate before publication.
- Preserve the existing artifact on download, decode, compile, verification,
  checksum, manifest, or rename failure.
- Avoid unresolved globs and broad recursive operations for cleanup. Delete
  only validated per-run temporary paths.
- Tests must use temporary directories and synthetic or license-compatible
  fixtures, never a production MMDB.

## Verification

Run validation proportional to the change. When Make is available, `make check`
is the preferred convenience entry point. It must validate without formatting
or otherwise rewriting tracked source. The underlying authoritative baseline
remains visible and directly runnable:

```text
find . -type f -name '*.go' -exec gofmt -l {} +
go mod tidy -diff
go list -m all
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
if [ -L bin ] || [ -L bin/stategeodb ]; then
  printf '%s\n' 'refusing to build through a symlink' >&2
  false
else
  mkdir -p bin
  go build -o bin/stategeodb ./cmd/stategeodb
fi
```

Use `make fmt` or `gofmt -w` as an explicit editing step, never as an acceptance
check. Also run focused tests, fuzz targets, integration fixtures, or benchmarks
when the phase changes prefix traversal, merging, MMDB encoding, filesystem
publication, concurrency, or memory use.

Every completed phase must provide evidence for its success criteria. Report:

- the behavior implemented;
- files and public interfaces changed;
- exact validation commands and results;
- benchmark or artifact-size changes when relevant;
- unresolved risks and the next safe phase.

Do not claim equivalence, determinism, atomicity, or memory improvement without
a test or measurement supporting the claim.

## Go implementation workflow

- Invoke every applicable installed Go skill before making or reviewing Go
  implementation decisions.
- For implementation phases, the primary agent must inspect and design the
  change, implement all code and tests itself, and run focused and repository
  validation before invoking the final read-only review batch.
- Use these exact Go subagents once as the final review batch when they are
  present: `go_reviewer` for correctness and compatibility, `go_concurrency`
  for lifecycle and synchronization, `go_security` for trust boundaries, and
  `go_performance` for measured efficiency. Give each agent a distinct task
  aligned with its configured specialty. An agent may report no actionable
  findings. Do not substitute, rename, invent, or repeatedly spawn the full
  group.
- The primary agent owns all implementation edits and consolidation. If a final
  reviewer finds an actionable issue, the primary agent makes the correction,
  reruns affected tests and validation, and may rerun only the reviewer whose
  material finding requires confirmation, at most once.
- Consult Context7 for current, version-sensitive, or unfamiliar Go and
  dependency APIs, and prefer primary or upstream documentation.
- Report the skills, subagents, and Context7 documentation actually used when a
  phase is completed. Report unavailable tooling honestly.
- Keep tool use relevant to the active phase and do not materially expand its
  scope.

## Documentation and Git

- Do not update `README.md` or `docs/ARCHITECTURE.md` during ordinary
  implementation phases. Change them only when the user explicitly requests
  documentation work.
- Record phase status, implementation evidence, temporary restrictions,
  dependency notes, and next steps in ignored local phase tracking. Package
  comments, exported API documentation, tests, and fixture provenance
  documentation remain allowed.
- Update `testdata/README.md` only when fixture provenance or licensing changes.
- Document material design decisions in an allowed durable location; do not
  leave the only explanation in a phase prompt.
- Do not commit `docs/PHASES.md`, `docs/phases/`, MMDB files, credentials, build
  reports containing source data, or local work directories.
- Preserve user changes and unrelated worktree state. Do not stage, commit,
  tag, push, or create releases unless explicitly requested.

## Phase completion reports

- Target at most 1,200 words unless a failure requires additional evidence.
- Report decisions and evidence, not a chronological work log.
- Do not paste complete successful test output, complete module graphs, raw
  coverage output, or exhaustive fixture tables unless directly changed by the
  phase.
- Include exact failure output for any required command that fails.
- Include enough information to reconstruct the public or cross-package API and
  judge the acceptance criteria.
- Use exactly these sections: `Result`, `Implementation`, `Tests and validation`,
  `Final subagent review`, and `Scope, risks, and next phase`.
