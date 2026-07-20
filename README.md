# stategeodb

`stategeodb` compiles one locally acquired MaxMind `GeoLite2-City` or
`GeoIP2-City` MMDB into a deterministic project MMDB for
`traefik-plugin-state-geo`. The output uses a fixed compliance artifact
profile: country is retained globally, while the first subdivision is retained
only for US records.

```text
country.iso_code
subdivisions[0].iso_code (US records only)
```

Non-US records are country-only even when the City source contains a
subdivision. Unknown-location networks remain present as empty records. This
projection contains geographic facts only; allow/deny policy remains in the
Traefik middleware.

Compilation is an offline build-time operation. Traefik continues to perform
local MMDB lookups without a request-time network, SQL, document-database,
cache-server, or CDN dependency.

## Current status

| Command | Status |
| --- | --- |
| `build` | Operational |
| `inspect` | Operational |
| `publish` | Operational |
| `compare` | Not implemented |
| `verify` | Not implemented |

`build` creates a temporary, verified candidate. `inspect` verifies project
metadata and structure and performs at most 32 selected lookups. `publish`
copies, hashes, and independently reverifies a temporary artifact before it
atomically creates, replaces, or leaves unchanged a stable artifact.
Publication creates no backup and does not delete the candidate.

## Requirements

- Go 1.26 or newer to build from source.
- macOS or Linux for local atomic publication.
- Make, optionally, for the convenience targets below.
- One locally acquired MaxMind City MMDB.
- Enough local memory for a full City build.
- Trusted operator-owned workspace and destination directories, with one
  publisher at a time per destination.

The current compliance-profile measurement used 5,831,951 normalized GeoLite
networks. The CLI build took 17.48 seconds and reached 2,791,964,672 bytes
(approximately 2.60 GiB) peak resident memory on the tested Apple Silicon
machine. It produced a 16,419,258-byte artifact with 2,736,008 tree nodes. For
the same source snapshot, the preceding generic 24-bit artifact was 24,475,735
bytes, so the compliance projection reduced it by 8,056,477 bytes (32.92%).
Allow at least approximately 4 GiB for operational headroom in the current
full-build workflow; that recommendation is not a measured minimum. These
figures are observations from one source snapshot, machine, and measurement
condition, not stable guarantees. Memory optimization is deferred.

## Data acquisition boundary

Source acquisition is outside `stategeodb`. Use MaxMind's supported
[GeoIP Update or download workflow](https://dev.maxmind.com/geoip/updating-databases/)
and keep credentials out of repository files.

- Do not commit production source MMDBs or generated production artifacts.
- Keep account IDs, license keys, authenticated URLs, and `GeoIP.conf` private.
- This repository contains only approved upstream test fixtures documented in
  [testdata/README.md](testdata/README.md).

The repository does not provide source-download automation.

## Build the CLI

```text
make build
```

The executable is written to:

```text
dist/bin/stategeodb
```

The corresponding raw Go build is:

```text
go build -o dist/bin/stategeodb ./cmd/stategeodb
```

Unlike `make build`, the raw command does not reject symlinked `dist` paths. Use
it only in a trusted worktree.

## Local v1 workflow

### Prepare directories

```text
mkdir -p tmp/work
```

The CLI requires `--workspace-root` to name an existing absolute directory, so
the examples below use `"$PWD/tmp/work"`.

### Choose a build epoch

`--build-epoch` is an explicit positive Unix timestamp embedded in the output
artifact. For an ad hoc build, obtain one with:

```text
date +%s
```

Using the same logical source and the same build epoch supports byte-identical
reproduction. Preserve a fixed value when reproducibility matters.

### Build a candidate

```text
dist/bin/stategeodb build \
  --source tmp/real/GeoLite2-City.mmdb \
  --source-id geolite2-city \
  --workspace-root "$PWD/tmp/work" \
  --build-epoch <unix-seconds>
```

Successful output contains exactly these six keys:

```text
candidate_path
input_records
output_networks
compared_segments
size_bytes
build_epoch
```

`candidate_path` identifies the verified MMDB. The candidate remains in its
private workspace after success; capture the value without evaluating the
output as shell code.

### Inspect a candidate

Metadata only:

```text
dist/bin/stategeodb inspect \
  --database <candidate_path>
```

Selected IPv4 and IPv6 lookups:

```text
dist/bin/stategeodb inspect \
  --database <candidate_path> \
  --ip 8.8.8.8 \
  --ip 2001:4860:4860::8888
```

Inspection requires exact stategeodb compliance schema-v1 metadata and rejects
selected lookup records that violate the profile. It never performs a full data
dump.

### Publish the stable local artifact

```text
make publish-artifact CANDIDATE="<candidate_path>"
```

The default destination is:

```text
dist/artifacts/stategeodb.mmdb
```

Publication reports one of three actions:

- absent destination: `created`;
- exact same bytes: `unchanged`;
- different verified bytes: `replaced`.

Replacement uses a verified temporary sibling and an atomic rename on the
supported local macOS and Linux filesystems. The current publisher has no
cross-process lock: use an operator-owned destination parent and serialize
publication per destination. No backup is created, and the candidate is
retained.

The explicit CLI equivalent is:

```text
dist/bin/stategeodb publish \
  --candidate <candidate_path> \
  --destination dist/artifacts/stategeodb.mmdb
```

When using the CLI directly, create `dist/artifacts` first; `publish` requires
the destination parent directory to exist.

### Inspect the stable artifact

```text
make inspect-artifact
```

For a selected lookup:

```text
dist/bin/stategeodb inspect \
  --database dist/artifacts/stategeodb.mmdb \
  --ip 8.8.8.8
```

### Transfer boundary

Copying or synchronizing the artifact into a Longhorn PVC is outside this
repository. The stable output can be consumed by a separate trusted
synchronization workflow, but `stategeodb` does not currently provide or claim
Kubernetes manifests, Longhorn synchronization, Traefik reload orchestration,
or cluster rollback.

## Integrity and safety guarantees

The implemented workflow provides these bounded guarantees when the workspace
and publication directories are exclusively controlled by the operator during
an invocation, candidate and temporary files are not concurrently modified,
and publication is serialized per destination:

- supported source metadata and MMDB structure are verified before ingestion;
- the normalized model and compiler cover native IPv4 and IPv6, including
  canonical mapped-IPv4 handling;
- output uses the fixed compliance profile: global country, US-only first
  subdivision, and preserved unknown-location records;
- identical logical input and a fixed build epoch produce byte-identical MMDB
  output;
- complete interval-based projected-source/output lookup equivalence is
  checked;
- a failed compile returns no candidate and attempts name-scoped cleanup inside
  the bound workspace root;
- inspection output is bounded to metadata and at most 32 addresses;
- publication copies, hashes, and verifies a temporary artifact before commit;
- exact byte comparison determines no-op publication;
- a temporary sibling plus rename prevents partial destination visibility;
- normal CLI diagnostics redact paths and raw parser details;
- process status is `0` for success and `1` for failure.

Atomic replacement depends on supported local macOS/Linux filesystem semantics.
The publisher syncs the temporary file but does not claim directory-entry
power-loss durability. Command stdout is trustworthy only with status `0`; an
output-write failure may leave a partial result.

## Current limitations

- One City source per build.
- Only exact `GeoLite2-City` and `GeoIP2-City` source types.
- No Enterprise support.
- No custom corrections or overrides.
- No secondary-source comparison or merging.
- No standalone `verify` operation.
- No JSON reports or manifests.
- No source acquisition.
- No automatic candidate cleanup after a successful build.
- No backup or rollback.
- No Kubernetes or PVC synchronization.
- High builder memory use.
- Publication only on macOS and Linux.
- Output schema fixed to project schema v1.

Artifacts using the former `StateGeo-Country-Subdivision` identity are not
compatible. Rebuild them from the licensed City source; inspection and
publication intentionally reject them rather than applying a compatibility
path.

## Development

```text
make help
make check
make build
make artifact-path
```

`make check` runs formatting checks, module consistency checks, uncached normal
tests, race-enabled tests, `go vet`, and a CLI build. Generated outputs live
under ignored `dist/`; private local inputs and workspaces live under ignored
`tmp/`. Tests use approved fixtures and synthetic data and do not require a
production GeoLite database.

## Future development

See the [public roadmap](docs/ROADMAP.md). The next priority is reviewed custom
CIDR location corrections, followed by independent verification and reporting,
then cautious secondary-source research.

## Architecture and licensing

- [Implemented architecture](docs/ARCHITECTURE.md)
- [Public roadmap](docs/ROADMAP.md)
- [Test fixture provenance](testdata/README.md)
- [MaxMind GeoLite data and licensing resources](https://dev.maxmind.com/geoip/geolite2-free-geolocation-data/)
- [MaxMind database update guidance](https://dev.maxmind.com/geoip/updating-databases/)
- [Pinned MaxMind DB reader](https://github.com/oschwald/maxminddb-golang/tree/v2.4.1)
- [Pinned MaxMind MMDB writer](https://github.com/maxmind/mmdbwriter/tree/v1.2.0)

Operators remain responsible for source-license, update, attribution, and
redistribution obligations. Transforming a database does not grant new
redistribution rights.
