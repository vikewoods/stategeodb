package maxmind

import (
	"context"
	"fmt"
	"net/netip"
	"slices"

	"github.com/vikewoods/stategeodb/internal/source"
)

// Records traverses every data-bearing network in the verified database and
// returns normalized records in source.Compare order. The same sourceID is
// applied to every record and is validated before traversal starts.
//
// Records returns no partial result on cancellation or failure. Close must not
// run concurrently with Records. Cancellation is checked between records and
// around sorting, but cannot interrupt one DecodePath or sorting operation.
func (d *Database) Records(ctx context.Context, sourceID string) ([]source.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := source.ValidateSourceID(sourceID); err != nil {
		return nil, fmt.Errorf("validate source id: %w", err)
	}
	if d == nil || d.reader == nil {
		return nil, newClassifiedError("read source records", ErrUnavailable)
	}

	records := make([]source.Record, 0)
	for result := range d.reader.Networks() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := result.Err(); err != nil {
			return nil, newClassifiedError("traverse source records", ErrIngest)
		}

		record, err := decodeCityRecord(result.Prefix(), sourceID, result.DecodePath)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	slices.SortFunc(records, source.Compare)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateSortedUniquePrefixes(records); err != nil {
		return nil, err
	}

	return records, nil
}

func validateSortedUniquePrefixes(records []source.Record) error {
	for i := 1; i < len(records); i++ {
		if records[i-1].Prefix == records[i].Prefix {
			return newClassifiedError("duplicate normalized source prefix", ErrIngest)
		}
	}
	return nil
}

func decodeCityRecord(
	prefix netip.Prefix,
	sourceID string,
	decodePath func(any, ...any) error,
) (source.Record, error) {
	var country *string
	if err := decodePath(&country, "country", "iso_code"); err != nil {
		return source.Record{}, newClassifiedError("decode country code", ErrIngest)
	}

	var subdivision *string
	if err := decodePath(&subdivision, "subdivisions", 0, "iso_code"); err != nil {
		return source.Record{}, newClassifiedError("decode subdivision code", ErrIngest)
	}

	record, err := source.NewRecord(
		prefix,
		stringValue(country),
		stringValue(subdivision),
		sourceID,
	)
	if err != nil {
		return source.Record{}, fmt.Errorf("normalize source record: %w", err)
	}

	return record, nil
}

func stringValue(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
