# Test fixture provenance

Committed test fixtures must be synthetic or explicitly license-compatible
upstream fixtures. Production GeoLite, GeoIP, DB-IP, customer, or other licensed
datasets must never be copied into this repository.

Synthetic network examples must use reserved documentation ranges:

- `192.0.2.0/24`, `198.51.100.0/24`, or `203.0.113.0/24` for IPv4;
- `2001:db8::/32` for IPv6.

Every checked-in binary fixture must have an entry in this document recording:

- repository-relative path;
- origin;
- license;
- generator and version, or exact upstream source;
- intended test cases;
- SHA-256 checksum.

Fixture generation must not require production credentials. Generated fixtures
should be reproducible where practical, with generation inputs and commands
documented alongside their provenance. An approved binary fixture also requires
an exact `.gitignore` allow-list entry; broad MMDB exceptions are prohibited.

## Binary fixture inventory

Phase 1.2 uses the following three upstream fixtures. They come from
[`maxmind/MaxMind-DB`](https://github.com/maxmind/MaxMind-DB) commit
`40d0b4ff0ffdad191e83bd8045b780dd052650e0`, the exact test-data submodule
revision referenced by `maxminddb-golang` `v2.4.1`. MaxMind-DB offers these
files under Apache-2.0 or MIT; this repository redistributes the selected files
under the MIT option and includes the upstream copyright statement at
`testdata/maxmind/NOTICE` and license terms at
`testdata/maxmind/LICENSE-MIT`.

### `testdata/maxmind/GeoIP2-City-Test.mmdb`

- Original path: `test-data/GeoIP2-City-Test.mmdb`
- License: MIT, with the upstream copyright and license notices included
  alongside the fixture
- Classification: valid supported `GeoIP2-City` database
- Purpose: prove successful open, exact metadata compatibility, full structural
  verification, reader ownership, close behavior, and defensive metadata copies
- SHA-256: `ed972738e4e03a3e56e12041a6af4d91592249d110f7e4a647e5f2fa0e639c09`

### `testdata/maxmind/GeoIP2-Country-Test.mmdb`

- Original path: `test-data/GeoIP2-Country-Test.mmdb`
- License: MIT, with the upstream copyright and license notices included
  alongside the fixture
- Classification: structurally valid but unsupported `GeoIP2-Country` database
- Purpose: prove database-type incompatibility is classified as unsupported,
  not corrupt
- SHA-256: `b37601903448683d241af52893c8cbf0fed461e0cdebe0bfaca01891fdeb6db9`

### `testdata/maxmind/GeoIP2-City-Test-Broken-Double-Format.mmdb`

- Original path: `test-data/GeoIP2-City-Test-Broken-Double-Format.mmdb`
- License: MIT, with the upstream copyright and license notices included
  alongside the fixture
- Classification: supported City metadata that opens but is intentionally
  corrupt and fails full structural verification
- Purpose: prove verifier-stage corruption cannot return a usable database
- SHA-256: `a340a6871b8bee8351befb8ad26f5229453705dddee0e948a35a65916c931e9c`

The upstream MIT notice has SHA-256
`91276db973f25602d1aa43491f59cbc84cb88e6f151e1d0cc82a755563ce0195`.
The neighboring copyright notice has SHA-256
`14770046606bd1f463f0346254a04d093e9e1794b82ed79cb0e754f5e17c0812`.
