package source

import (
	"cmp"
	"net/netip"
)

// Record is one provider-neutral network location observation. Country and
// Subdivision may both be empty when a source knows the network but not its
// required location fields. Record contains geographic facts, never policy.
type Record struct {
	Prefix      netip.Prefix
	Country     string
	Subdivision string
	SourceID    string
}

// NewRecord validates and normalizes one source observation.
func NewRecord(
	prefix netip.Prefix,
	country string,
	subdivision string,
	sourceID string,
) (Record, error) {
	normalizedPrefix, err := NormalizePrefix(prefix)
	if err != nil {
		return Record{}, err
	}

	normalizedCountry, normalizedSubdivision, err := NormalizeLocation(country, subdivision)
	if err != nil {
		return Record{}, err
	}

	if err := ValidateSourceID(sourceID); err != nil {
		return Record{}, err
	}

	return Record{
		Prefix:      normalizedPrefix,
		Country:     normalizedCountry,
		Subdivision: normalizedSubdivision,
		SourceID:    sourceID,
	}, nil
}

// Validate reports whether record already satisfies every normalized record
// invariant. It does not normalize or otherwise mutate record.
func (record Record) Validate() error {
	normalizedPrefix, err := NormalizePrefix(record.Prefix)
	if err != nil {
		return err
	}
	if normalizedPrefix != record.Prefix {
		return invalid(ErrInvalidPrefix, "value is not canonical")
	}

	normalizedCountry, normalizedSubdivision, err := NormalizeLocation(
		record.Country,
		record.Subdivision,
	)
	if err != nil {
		return err
	}
	if normalizedCountry != record.Country {
		return invalid(ErrInvalidCountry, "value is not normalized")
	}
	if normalizedSubdivision != record.Subdivision {
		return invalid(ErrInvalidSubdivision, "value is not normalized")
	}

	if err := ValidateSourceID(record.SourceID); err != nil {
		return err
	}

	return nil
}

// Compare defines the total order for normalized records: IPv4 before IPv6,
// network address, prefix length, source ID, country, then subdivision.
func Compare(left Record, right Record) int {
	leftIsIPv4 := left.Prefix.Addr().Is4()
	rightIsIPv4 := right.Prefix.Addr().Is4()
	if leftIsIPv4 != rightIsIPv4 {
		if leftIsIPv4 {
			return -1
		}
		return 1
	}

	if result := left.Prefix.Addr().Compare(right.Prefix.Addr()); result != 0 {
		return result
	}
	if result := cmp.Compare(left.Prefix.Bits(), right.Prefix.Bits()); result != 0 {
		return result
	}
	if result := cmp.Compare(left.SourceID, right.SourceID); result != 0 {
		return result
	}
	if result := cmp.Compare(left.Country, right.Country); result != 0 {
		return result
	}

	return cmp.Compare(left.Subdivision, right.Subdivision)
}
