// Package artifact validates stategeodb metadata, structure, and normalized
// runtime fields. Unknown record fields are not currently rejected.
package artifact

import (
	"context"
	"errors"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/vikewoods/stategeodb/internal/mmdb"
	"github.com/vikewoods/stategeodb/internal/source"
)

var (
	// ErrUnsupported classifies MMDB metadata that does not identify the
	// supported stategeodb runtime schema.
	ErrUnsupported = errors.New("artifact: unsupported database")
	// ErrCorrupt classifies structural MMDB verification failures.
	ErrCorrupt = errors.New("artifact: corrupt database")
)

// Compatible reports whether metadata exactly matches the generated
// stategeodb schema and binary encoding contract.
func Compatible(metadata maxminddb.Metadata) bool {
	return metadata.DatabaseType == mmdb.DatabaseType &&
		len(metadata.Description) == 1 &&
		metadata.Description["en"] == mmdb.SchemaDescription &&
		len(metadata.Languages) == 0 &&
		metadata.BuildEpoch > 0 &&
		metadata.BinaryFormatMajorVersion == 2 &&
		metadata.BinaryFormatMinorVersion == 0 &&
		metadata.IPVersion == 6 &&
		metadata.RecordSize == 28 &&
		metadata.NodeCount > 0
}

// Verify validates exact project metadata, performs upstream complete
// structural verification, and checks normalized fields in every record. One
// active upstream Verify call cannot be interrupted in its middle.
func Verify(ctx context.Context, reader *maxminddb.Reader) error {
	if ctx == nil || reader == nil {
		return ErrCorrupt
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !Compatible(reader.Metadata) {
		return ErrUnsupported
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := reader.Verify(); err != nil {
		return ErrCorrupt
	}
	return verifyRuntimeRecords(ctx, reader)
}

func verifyRuntimeRecords(ctx context.Context, reader *maxminddb.Reader) error {
	const verificationSourceID = "artifact-verification"
	for result := range reader.Networks() {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := result.Err(); err != nil {
			return ErrCorrupt
		}
		prefix := result.Prefix()
		prefixOnly := source.Record{Prefix: prefix, SourceID: verificationSourceID}
		if err := prefixOnly.Validate(); err != nil {
			return ErrUnsupported
		}

		var country *string
		if err := result.DecodePath(&country, "country", "iso_code"); err != nil {
			return ErrUnsupported
		}
		var subdivision *string
		if err := result.DecodePath(&subdivision, "subdivisions", 0, "iso_code"); err != nil {
			return ErrUnsupported
		}
		rawCountry := optionalString(country)
		rawSubdivision := optionalString(subdivision)
		record, err := source.NewRecord(
			prefix,
			rawCountry,
			rawSubdivision,
			verificationSourceID,
		)
		if err != nil || record.Country != rawCountry || record.Subdivision != rawSubdivision {
			return ErrUnsupported
		}
	}
	return ctx.Err()
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
