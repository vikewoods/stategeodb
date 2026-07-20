# stategeodb agent guidance

## Purpose

`stategeodb` is a deterministic, automation-first Go CLI that compiles a
locally acquired MaxMind City MMDB into the minimal MMDB consumed by
`traefik-plugin-state-geo`.

Compilation is an offline build-time operation. Do not introduce a request-time
network, SQL database, document database, cache server, CDN, or external lookup
dependency into the Traefik request path.

The compiler owns geographic data transformation and artifact integrity.
Traefik owns runtime allow/deny policy.

## Repository map

* `cmd/stategeodb`: minimal executable entry point.
* `internal/cli`: command parsing, help, stdout/stderr contracts, and diagnostics.
* `internal/source`: normalized geographic records and source-neutral rules.
* `internal/source/maxmind`: verified MaxMind City source ingestion.
* `internal/mmdb`: deterministic runtime MMDB encoding.
* `internal/compiler`: candidate creation and behavioral equivalence.
* `internal/artifact`: generated-artifact verification.
* `internal/inspect`: bounded metadata and selected-address inspection.
* `internal/publish`: verified byte comparison and atomic local replacement.
* `testdata`: approved fixture provenance and licensing material only.
* `dist`: ignored generated CLI binaries and published local artifacts.
* `tmp`: ignored private inputs and operator-owned temporary workspaces.

Do not add generic `common`, `utils`, `helpers`, or `model` packages when a
specific owning package is available.

## Context routing

Inspect the relevant implementation and tests before changing code. Read
documentation according to the task instead of loading every document
unconditionally:

* `README.md`: current operator behavior, commands, requirements, and limitations.
* `docs/ARCHITECTURE.md`: implemented package ownership, runtime contracts,
  trust boundaries, determinism, verification, and publication semantics.
* `docs/ROADMAP.md`: durable future direction and feature priority.
* `testdata/README.md`: fixture provenance, licensing, and fixture rules.
* `go.mod`: Go version and pinned dependencies.
* `Makefile`: supported local build, validation, and artifact workflow.

Production code and tests are the source of truth when documentation conflicts
with implemented behavior.

Do not create execution journals, private phase trackers, generated planning
directories, or task histories in the repository. Use the current task context
unless the user explicitly requests a durable issue or document.

## Scope and authorization

* Reviews, explanations, diagnoses, and plans are read-only unless the user also
  requests implementation.
* Implementation requests authorize relevant in-repository edits and
  non-destructive validation.
* Require explicit authorization before:

  * staging, committing, tagging, or pushing Git changes;
  * publishing releases, packages, binaries, or container images;
  * deploying or changing live infrastructure;
  * modifying another repository;
  * deleting or overwriting user-owned data outside task-owned temporary paths;
  * downloading, copying, or redistributing licensed production data;
  * materially expanding the requested product scope.
* Preserve unrelated worktree changes.
* Do not revert, rewrite, format, move, or delete unrelated files.
* Keep live Kubernetes, Longhorn, and Traefik deployment resources in their
  owning repositories.
* This repository owns the local compiler and reviewed reference material, not
  live cluster state.

When scope is ambiguous, choose the smallest implementation that satisfies the
stated outcome.

## Product boundaries

* Keep geographic facts separate from access-control policy.
* Do not encode allow/deny decisions, blocked states, customer policy, or
  middleware configuration in MMDB records.
* Source acquisition remains external to the compiler unless explicitly added
  as a reviewed product feature.
* Longhorn PVC synchronization and Traefik reload orchestration remain outside
  this repository unless explicitly authorized as a later integration.
* Do not add speculative support for GeoIP2 Enterprise, secondary providers,
  Kubernetes Jobs, backups, rollback, or remote publication.

## Artifact contract

The active runtime artifact profile is defined by production constants,
verification code, tests, and `docs/ARCHITECTURE.md`.

Do not change the artifact profile implicitly.

Any change to retained geographic meaning must deliberately address:

* database identity;
* schema version or compatibility;
* writer encoding;
* artifact verification;
* compiler equivalence rules;
* inspect behavior;
* publication acceptance;
* Traefik consumer compatibility;
* documentation and migration behavior.

Geographic projection must remain explicit. For example, retaining subdivisions
only for a specific country is a semantic artifact-profile change, not a hidden
writer optimization.

Physical MMDB encoding settings are implementation details. Maintain one
canonical production encoding. Do not expose record size or similar physical
encoding choices as CLI flags without a concrete operator requirement.

Legacy or experimental encodings belong in tests or explicit migration tooling,
not in the normal production command surface.

## Durable invariants

* Cover native IPv4 and IPv6 behavior together.
* Use canonical IP prefixes.
* Preserve the distinction between:

  * an address absent from the database; and
  * a present network whose required geographic values are unknown.
* Identical logical input, artifact profile, configuration, and injected build
  time must produce byte-identical output.
* Source identity and provenance must not affect runtime MMDB bytes unless the
  artifact contract explicitly requires them.
* Do not return or publish partial compiler output.
* Do not publish a candidate until all required structural, schema, and
  behavioral verification passes.
* Verify the exact file snapshot consumed at each trust-sensitive boundary.
* Preserve deterministic ordering and conflict resolution.
* Do not silently deduplicate conflicting prefixes.
* Do not weaken exact behavioral equivalence to sampled lookup checks.
* Do not treat provider agreement as truth.
* Do not add a secondary source to production output before comparison,
  licensing review, and deterministic conflict policy exist.

## Publication invariants

* Use a temporary publication file beside the destination when atomic rename is
  required.
* Before the atomic publication commit, every failure must preserve the existing
  published artifact.
* The successful rename is the publication commit point.
* After the rename succeeds, later reporting, stdout, or cancellation failure
  must not roll back or delete the installed artifact.
* Verify the exact temporary artifact before rename.
* Determine unchanged publication through exact byte comparison, not checksum
  equality alone.
* Treat SHA-256 as artifact evidence, not as a replacement for exact comparison.
* Do not create backups, rollback state, retention history, or sidecar files
  unless explicitly requested.
* Do not delete a candidate merely because publication succeeds.
* Do not claim full directory-entry power-loss durability unless it is
  implemented and tested.

## Filesystem and data safety

* Use task-owned temporary directories for generated test and validation data.
* Do not recursively delete a path inferred from an untrusted string.
* Remove only paths whose ownership was established by the current operation.
* Prefer root-relative filesystem operations and explicit file-identity checks
  at trust-sensitive boundaries.
* Reject symbolic links and non-regular files where the operation requires a
  stable regular-file snapshot.
* Use exclusive creation for temporary or candidate files where collisions
  matter.
* Keep generated distribution output under ignored `dist/`.
* Keep private MMDB sources and temporary operator workspaces under ignored
  `tmp/`.
* Never stage or commit licensed production MMDB data.

Tracked tests and default validation must use temporary directories and
synthetic or approved fixtures. They must not require licensed production data.

Opt-in local validation may use an explicitly authorized, Git-ignored production
MMDB. It must:

* skip cleanly when the opt-in input is absent;
* avoid copying, renaming, modifying, redistributing, staging, or committing it;
* avoid revealing the complete private source path;
* avoid hardcoding changing source checksums, sizes, epochs, or record counts in
  tracked assertions;
* clean all generated candidates, profiles, traces, and workspaces.

## Secrets and diagnostic safety

Do not log or expose:

* credentials;
* account identifiers;
* licence keys;
* authenticated URLs;
* private source paths;
* production source archives;
* raw MMDB contents;
* decoded record dumps;
* parser offsets that expose unnecessary internals;
* real client IP samples;
* untrusted argument values without validation and redaction.

Successful commands may report explicitly requested output artifact paths.

Tests and documentation may use reserved documentation ranges or well-known
public test addresses where appropriate.

Preserve useful classifications with `errors.Is`, but do not expose unsafe raw
causes merely to improve diagnostics.

Prefer:

* a stable error classification;
* a safe default diagnostic;
* a sanitized cause where available;
* optional bounded detail when a reviewed detailed-output mode exists.

Do not discard safe standard classifications such as missing-file or permission
errors without a concrete reason.

## Go conventions

* Use the Go version declared by `go.mod`.
* Keep `cmd/stategeodb` and `main` minimal.
* Keep argument parsing and output formatting in `internal/cli`.
* Keep domain behavior independently testable outside the CLI.
* Use `net/netip` for IP addresses and prefixes.
* Propagate `context.Context` through blocking or cancellable operations.
* Do not add context parameters to operations that cannot meaningfully observe
  cancellation.
* Do not spawn goroutines merely to simulate cancellation around a synchronous
  dependency call.
* Use explicit ownership for files, readers, workspaces, and candidates.
* Prefer value types for immutable data and pointer ownership for resources.
* Prefer constructors and boundary validation for normalized data.
* Prefer narrow concrete APIs over speculative interfaces.
* Add an interface only when there are multiple real implementations or a
  concrete test boundary that cannot remain unexported.
* Prefer the standard library.
* Add production dependencies only for a concrete correctness, security,
  maintenance, or ownership advantage.
* Avoid mutable package-global build or command state.
* Each invocation owns its inputs, context, workspace, resources, and result.
* Do not use finalizers for correctness or cleanup.
* Do not call `os.Exit` below `main`; return statuses or errors so cleanup runs.
* Avoid reflection for cases that can be prevented through clear internal API
  ownership.
* Avoid clever abstractions that reduce line count while obscuring lifecycle,
  trust, or error behavior.

## CLI conventions

* Keep commands non-interactive and suitable for unattended automation.
* Preserve the current explicit command inputs unless a reviewed configuration
  system is introduced.
* Do not infer paths, source identity, build epoch, or publication destinations
  from the working directory, filenames, environment, or current time unless
  the command contract explicitly documents that behavior.
* Write stable command results and help to stdout.
* Write diagnostics to stderr.
* Successful machine-consumable output must be deterministic and documented.
* Failed commands must not emit misleading normal results.
* Build complete bounded output before writing when partial successful output
  would be unsafe or ambiguous.
* Preserve the current process contract:

  * `0`: success;
  * `1`: failure.
* Broaden exit statuses only when a concrete automation requirement justifies a
  stable taxonomy.
* Do not require scripts to parse unstable upstream error text.
* Keep physical artifact-encoding options out of the operator CLI unless a real
  consumer needs them.

## Skills and research

All enabled skills remain available through their trigger descriptions. Load
only the smallest set that matches the task.

Typical routing:

* CLI contracts: `golang-cli`.
* Unit, integration, fixture, fuzz, race, or flaky tests: `golang-testing`.
* Cancellation and context boundaries: `golang-context`.
* Errors and diagnostics: `golang-error-handling`.
* Filesystem boundaries, untrusted input, publication, secrets, or licensing:
  `golang-security` and, where relevant, `golang-safety`.
* Semantic navigation and safe refactoring: `golang-gopls` and
  `golang-refactoring`.
* Performance or memory investigation: `golang-benchmark` first, followed by
  `golang-performance` after identifying a measured bottleneck.
* Module or dependency changes: `golang-dependency-management`.
* Project structure: `golang-project-layout`.
* Public or package documentation: `golang-documentation`.

Other installed skills may trigger when their descriptions match.

Do not invoke every Go skill by default.

Do not force database, gRPC, Fx, observability, CI, container, or Kubernetes
skills onto unrelated work.

Prefer evidence in this order:

1. current production code and tests;
2. `gopls` and local Go tooling;
3. pinned dependency source and tests;
4. official Go or upstream documentation;
5. Context7 or web research when current external verification is necessary.

When external verification is necessary and the tool is available, use Context7
or web search for current, version-sensitive, unfamiliar, or externally defined
behavior that cannot be resolved locally.

Prefer primary sources.

If external tooling is unavailable or incomplete, use the strongest local or
upstream source available and report the limitation honestly.

Do not claim a tool, skill, source, or review was used when it was not.

## Subagents

Default documentation changes, routine fixes, and narrow low-risk package work
to the primary agent only.

Use a specialist only when the task materially exercises that specialty or when
independent review would reduce a concrete risk.

Available read-only specialists:

* `go_reviewer`: correctness, compatibility, error handling, API contracts, and
  missing tests.
* `go_concurrency`: goroutines, cancellation, synchronization, resource
  ownership, and lifecycle.
* `go_security`: untrusted input, filesystem boundaries, secrets, licensing,
  publication, and data handling.
* `go_performance`: measured compiler, MMDB, allocation, memory, and benchmark
  behavior.

Rules:

* Do not run all four by default.
* Select only relevant specialists.
* Give each selected agent one bounded, self-contained review or investigation.
* Do not use subagents to duplicate primary-agent implementation.
* Do not allow multiple agents to edit shared files concurrently.
* Subagents remain read-only; the primary agent owns all edits.
* Do not ask subagents to delegate further.
* For implementation tasks, normally complete implementation and focused
  validation before requesting specialist final review.
* For investigation-only tasks, read-heavy specialist analysis may run earlier
  when it reduces uncertainty.
* Wait for selected agents before finalizing.
* Consolidate findings in the primary thread.
* If a finding is corrected, rerun only the relevant reviewer when confirmation
  is materially useful.
* A specialist may report that no actionable issue exists.
* Do not manufacture findings or work merely to demonstrate delegation.

## Testing conventions

* Test externally meaningful behavior rather than private implementation shape.
* Use table-driven tests when they make contracts clearer.
* Preserve `errors.Is` classifications in tests.
* Test failure cleanup, cancellation, ownership, and repeated invocation where
  relevant.
* Avoid timing-dependent sleeps when deterministic seams can model the
  boundary.
* Do not add mutable package-global test hooks.
* Keep test seams unexported where possible.
* Do not duplicate complete lower-layer test suites in integration packages.
* Add regression tests for every corrected material defect.
* Test deterministic output across separate processes when process state could
  affect bytes.
* Test atomic publication at the commit boundary.
* Do not weaken adversarial filesystem tests without documenting the changed
  threat model.
* Do not add benchmarks without a defined workload and metric.
* Do not convert benchmark observations into pass/fail latency thresholds
  without a concrete service-level requirement.
* Distinguish:

  * cumulative allocated bytes;
  * live Go heap;
  * process RSS;
  * peak resident memory;
  * mapped file pages.

Do not describe `B/op` as peak memory.

## Validation

Run validation proportional to the change.

During implementation:

* format changed Go files explicitly;
* run focused tests for changed packages;
* use `make fmt-check` as the non-mutating formatting check;
* run focused race tests when lifecycle, shared state, or cancellation changes;
* run focused integration or adversarial tests when filesystem or publication
  behavior changes.

Before completing non-trivial Go changes:

```text
make check
```

`make check` is the preferred convenience entry point. Its underlying Go
commands remain authoritative.

Also run additional focused validation when the change affects:

* determinism;
* artifact compatibility;
* source/output equivalence;
* publication atomicity;
* private-data boundaries;
* real-dataset behavior;
* benchmarks or memory claims.

Documentation-only changes do not require the full Go suite unless:

* command examples changed;
* code-generated output is documented;
* a current-behavior claim needs execution to verify;
* repository validation rules require it.

Do not silently format code as part of acceptance validation.

If a relevant check cannot run:

* report the exact command;
* report the exact reason;
* distinguish code failure from sandbox, cache, network, permission, platform,
  or tooling limitations;
* do not imply the check passed.

Report only validation that actually ran.

## Documentation ownership

Update documentation only when its owned contract changes:

* `README.md`: user-visible behavior, prerequisites, commands, workflow, and
  limitations.
* `docs/ARCHITECTURE.md`: implemented ownership, artifact contracts, trust
  boundaries, verification, determinism, performance model, and publication
  guarantees.
* `docs/ROADMAP.md`: durable future direction and priority.
* `testdata/README.md`: fixture provenance, licensing, checksums, and fixture
  purpose.

Do not:

* use public documentation as an implementation journal;
* add task-by-task completion history;
* recreate private phase-tracking files;
* document planned functionality as implemented;
* broadly rewrite unrelated documentation during a narrow code change;
* add changing private-source measurements to tracked documentation unless they
  are intentionally generalized and reviewed.

Implementation prompts, tool logs, and acceptance evidence remain task artifacts
unless explicitly promoted into a durable public document or issue.

Use these labels accurately:

* implemented;
* not implemented;
* planned;
* outside this repository.

## Completion reports

Keep completion reports concise and evidence-based.

Include:

* result;
* material behavior, interface, or contract changes;
* files or packages changed;
* validation actually performed;
* relevant specialist findings;
* remaining risks, limitations, or follow-up work.

Do not include:

* chronological tool activity;
* complete successful logs;
* full module graphs without a dependency conflict;
* repeated acceptance criteria;
* claims that a command passed when it did not run;
* hidden reasoning.

State failures and unresolved criteria directly.

Distinguish implementation defects from environment or tooling limitations.
