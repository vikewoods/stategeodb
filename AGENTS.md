# Repository guidance

## Mission

Build `stategeodb` as a deterministic, automation-first Go CLI that compiles
licensed geolocation sources into a minimal MMDB for local use by
`traefik-plugin-state-geo`.

The CLI is an offline build tool. It is not a request-time service and must not
introduce a network, SQL, document-database, cache-server, or CDN dependency
into Traefik's request path.

## Read before changing code

1. Read `README.md` for current operator behavior and project boundaries.
2. Read `docs/ARCHITECTURE.md` for implemented ownership and durable decisions.
3. Read the relevant priorities and constraints in `docs/ROADMAP.md`.
4. Inspect the current code and tests before selecting packages or changing an
   interface.

Task-level plans, acceptance criteria, and status belong in GitHub issues or
another explicitly approved tracker when the work needs a durable public
record. Do not create repository-local execution journals or private planning
files. Prompts and transient validation evidence remain in the working
conversation unless the user asks to record them in an issue or durable public
document.

## Authorization boundaries

- For review, explanation, diagnosis, or planning requests, inspect relevant
  files and report findings. Do not implement changes unless requested.
- For build, change, or fix requests, make the requested in-repository edits and
  run relevant non-destructive validation without pausing for routine approval.
- Require explicit confirmation before publishing images or releases, pushing
  Git changes, deploying to a cluster, writing to another repository, deleting
  material data, rotating credentials, or materially expanding the requested
  scope.
- Never download or redistribute licensed production data unless the task
  explicitly authorizes that source and use.

## Project boundaries

- Keep the compiler in this repository and the Traefik middleware in its own
  repository. Do not copy writer or CLI dependencies into the Yaegi plugin.
- Keep live Kubernetes resources in their cluster repositories. This repository
  may contain reviewed reference schemas and examples only when explicitly
  requested.
- Keep geographic data separate from access-control policy. Location
  corrections may change country or subdivision facts; allow/deny rules belong
  to middleware or cluster policy.
- Keep generated databases, credentials, source archives, build workspaces, and
  private operator data out of Git.

## Go and CLI conventions

- Use the Go version declared by `go.mod`.
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

- Commands must be non-interactive and safe for shell pipelines and unattended
  local automation.
- Machine-readable results go to stdout. Logs and diagnostics go to stderr.
- Add explicit JSON output only when a report or inspection operation has a
  reviewed structured-result contract; current help and version output remains
  text-only.
- Invalid flags or configuration are usage failures; corrupt inputs,
  verification failures, and publication failures are distinguishable through
  diagnostics and, when domain behavior exists, internal error classification.
- The current process-status contract is binary: `0` for success and `1` for
  failure. Change it only for a concrete automation requirement.
- Do not call `os.Exit` below `main`; return errors so deferred cleanup runs.
- Do not print credentials, authenticated URLs, source archives, or client IP
  samples in normal logs.
- Blocking operations must honor caller cancellation, and cancellation must
  leave the current published MMDB unchanged.

## Data invariants

- IPv4 and IPv6 behavior must be covered by the same feature and test work.
- Normalize runtime output to `country.iso_code` and optional
  `subdivisions[0].iso_code` unless architecture is deliberately revised.
- Identical logical inputs, configuration, and injected build time must produce
  byte-identical output.
- Never replace the published artifact until the complete candidate passes all
  configured structural and behavioral gates.
- Provider disagreement is evidence of uncertainty, not proof that either
  provider is correct.

If corrections are implemented, longest prefix wins and duplicate conflicting
prefixes are build errors. Apply corrections after provider normalization and
record their provenance in build evidence. If fallback merging is implemented,
primary values remain authoritative and a secondary source may fill only
missing data under an explicit configured policy.

## Filesystem and publication safety

- Create publication temporaries beside the final destination when atomic
  rename is required.
- Reopen and verify the exact candidate snapshot before publication.
- Preserve the existing artifact on decode, compile, verification, checksum,
  copy, or rename failure.
- Avoid unresolved globs and broad recursive cleanup. Delete only validated
  per-run temporary paths.
- Tests must use temporary directories and synthetic or license-compatible
  fixtures, never a production MMDB.

## Verification

Run validation proportional to the change. When Make is available, `make check`
is the preferred convenience entry point. It must validate without formatting
or otherwise rewriting tracked source. The underlying baseline remains visible
and directly runnable:

```text
find . \( -path './.git' -o -path './bin' -o -path './dist' -o -path './tmp' -o -path './work' \) -prune -o -type f -name '*.go' -exec gofmt -l {} +
go mod tidy -diff
go list -m all
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
if [ -L dist ] || [ -L dist/bin ] || [ -L dist/bin/stategeodb ]; then
  printf '%s\n' 'refusing to build through a symlink' >&2
  false
else
  mkdir -p dist/bin
  go build -o dist/bin/stategeodb ./cmd/stategeodb
fi
```

Use `make fmt` or `gofmt -w` as an explicit editing step, never as an acceptance
check. Also run focused tests, fuzz targets, integration fixtures, or benchmarks
when work changes prefix traversal, merging, MMDB encoding, filesystem
publication, concurrency, or memory use.

Completed implementation work must report behavior, files and public interfaces
changed, exact validation commands and results, benchmark or artifact-size
changes when relevant, and unresolved risks with the next safe task. Do not
claim equivalence, determinism, atomicity, or memory improvement without a test
or measurement supporting the claim.

## Go implementation workflow

- Invoke every applicable installed Go skill before making or reviewing Go
  implementation decisions.
- For implementation tasks, the primary agent must inspect and design the
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
  task is completed. Report unavailable tooling honestly.
- Keep tool use relevant to the active work and do not materially expand its
  scope.

## Documentation ownership and Git

- Update `README.md` when current user-facing behavior, commands, requirements,
  or limitations change.
- Update `docs/ARCHITECTURE.md` when implemented component ownership, contracts,
  trust boundaries, or durable decisions change.
- Update `docs/ROADMAP.md` when public direction or priority changes; do not use
  it as a task journal.
- Update `testdata/README.md` only when fixture provenance or licensing changes.
- Package comments, exported API documentation, tests, and fixture provenance
  remain allowed during implementation. Do not rewrite the main public docs for
  unrelated code work.
- Preserve user changes and unrelated worktree state. Do not stage, commit,
  tag, push, or create releases unless explicitly requested.

## Task completion reports

- Target at most 1,200 words unless a failure requires additional evidence.
- Report decisions and evidence, not a chronological work log.
- Do not paste complete successful test output, complete module graphs, raw
  coverage output, or exhaustive fixture tables unless directly changed.
- Include exact failure output for any required command that fails.
- Include enough information to reconstruct the public or cross-package API and
  judge the acceptance criteria.
- Unless a user supplies another required format, use these sections: `Result`,
  `Implementation`, `Tests and validation`, `Final subagent review`, and `Scope,
  risks, and next task`.
