// Package artifactprofile defines the fixed geographic projection accepted by
// stategeodb runtime artifacts.
package artifactprofile

import (
	"errors"

	"github.com/vikewoods/stategeodb/internal/source"
)

// ErrInvalidRecord classifies records that do not satisfy the compliance
// artifact profile or the underlying normalized source-record invariants.
var ErrInvalidRecord = errors.New("artifact profile: invalid record")

// Project returns a value copy of record with subdivisions retained only for
// US locations. The complete source record is validated before projection.
func Project(record source.Record) (source.Record, error) {
	if err := record.Validate(); err != nil {
		return source.Record{}, invalidRecordError{cause: err}
	}

	projected := record
	if projected.Country != "US" {
		projected.Subdivision = ""
	}
	return projected, nil
}

// Validate reports whether record is normalized and contains a subdivision
// only when its country is US. It does not normalize or mutate record.
func Validate(record source.Record) error {
	if err := record.Validate(); err != nil {
		return invalidRecordError{cause: err}
	}
	if record.Country != "US" && record.Subdivision != "" {
		return ErrInvalidRecord
	}
	return nil
}

type invalidRecordError struct {
	cause error
}

func (err invalidRecordError) Error() string {
	return ErrInvalidRecord.Error()
}

func (err invalidRecordError) Unwrap() []error {
	return []error{ErrInvalidRecord, err.cause}
}
