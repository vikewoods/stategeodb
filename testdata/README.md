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

Phase 1.1 introduces no MMDB reader, generator, or binary fixture. The current
binary fixture inventory is empty.
