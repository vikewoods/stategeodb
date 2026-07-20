package artifactprofile_test

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/vikewoods/stategeodb/internal/artifactprofile"
	"github.com/vikewoods/stategeodb/internal/source"
)

func TestProject(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		record              source.Record
		expectedSubdivision string
	}{
		{name: "US subdivision retained", record: mustRecord(t, "192.0.2.0/24", "US", "CA"), expectedSubdivision: "CA"},
		{name: "US without subdivision retained", record: mustRecord(t, "192.0.2.0/24", "US", "")},
		{name: "non-US subdivision removed", record: mustRecord(t, "2001:db8::/32", "GB", "ENG")},
		{name: "non-US without subdivision unchanged", record: mustRecord(t, "2001:db8::/32", "GB", "")},
		{name: "unknown location retained", record: mustRecord(t, "198.51.100.0/24", "", "")},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			before := test.record
			projected, err := artifactprofile.Project(test.record)
			if err != nil {
				t.Fatalf("Project() error = %v", err)
			}
			if test.record != before {
				t.Errorf("Project() mutated input: got %+v, want %+v", test.record, before)
			}
			if projected.Prefix != before.Prefix || projected.Country != before.Country ||
				projected.SourceID != before.SourceID || projected.Subdivision != test.expectedSubdivision {
				t.Errorf("Project() = %+v, want original fields and subdivision %q", projected, test.expectedSubdivision)
			}
			repeated, err := artifactprofile.Project(projected)
			if err != nil {
				t.Fatalf("Project(projected) error = %v", err)
			}
			if repeated != projected {
				t.Errorf("Project() is not idempotent: first %+v, second %+v", projected, repeated)
			}
		})
	}
}

func TestProject_ValidatesBeforeProjection(t *testing.T) {
	t.Parallel()

	record := mustRecord(t, "192.0.2.0/24", "GB", "ENG")
	record.Country = "gb"
	projected, err := artifactprofile.Project(record)
	if projected != (source.Record{}) {
		t.Errorf("Project() = %+v, want zero record", projected)
	}
	for _, target := range []error{artifactprofile.ErrInvalidRecord, source.ErrInvalidCountry} {
		if !errors.Is(err, target) {
			t.Errorf("Project() error = %v, want errors.Is(%v)", err, target)
		}
	}
	if strings.Contains(err.Error(), record.Country) {
		t.Errorf("Project() error exposed record value: %v", err)
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()

	valid := []source.Record{
		mustRecord(t, "192.0.2.0/24", "US", "CA"),
		mustRecord(t, "192.0.2.0/24", "US", ""),
		mustRecord(t, "2001:db8::/32", "GB", ""),
		mustRecord(t, "198.51.100.0/24", "", ""),
	}
	for _, record := range valid {
		before := record
		if err := artifactprofile.Validate(record); err != nil {
			t.Errorf("Validate(%+v) error = %v", record, err)
		}
		if record != before {
			t.Errorf("Validate() mutated record: got %+v, want %+v", record, before)
		}
	}

	nonUSSubdivision := mustRecord(t, "2001:db8::/32", "GB", "ENG")
	if err := artifactprofile.Validate(nonUSSubdivision); !errors.Is(err, artifactprofile.ErrInvalidRecord) {
		t.Errorf("Validate(non-US subdivision) error = %v, want ErrInvalidRecord", err)
	}

	invalid := mustRecord(t, "192.0.2.0/24", "US", "CA")
	invalid.Subdivision = "ca"
	err := artifactprofile.Validate(invalid)
	for _, target := range []error{artifactprofile.ErrInvalidRecord, source.ErrInvalidSubdivision} {
		if !errors.Is(err, target) {
			t.Errorf("Validate(invalid) error = %v, want errors.Is(%v)", err, target)
		}
	}
	if strings.Contains(err.Error(), invalid.Subdivision) {
		t.Errorf("Validate() error exposed record value: %v", err)
	}
}

func mustRecord(t *testing.T, prefix string, country string, subdivision string) source.Record {
	t.Helper()
	record, err := source.NewRecord(
		netip.MustParsePrefix(prefix),
		country,
		subdivision,
		"profile-test",
	)
	if err != nil {
		t.Fatalf("NewRecord() error = %v", err)
	}
	return record
}
