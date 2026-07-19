package source_test

import (
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/vikewoods/stategeodb/internal/source"
)

func TestNewRecord(t *testing.T) {
	t.Parallel()

	record, err := source.NewRecord(
		netip.MustParsePrefix("::ffff:192.0.2.129/120"),
		"us",
		"ca",
		"Primary-City",
	)
	if err != nil {
		t.Fatalf("NewRecord() error = %v", err)
	}

	expected := source.Record{
		Prefix:      netip.MustParsePrefix("192.0.2.0/24"),
		Country:     "US",
		Subdivision: "CA",
		SourceID:    "Primary-City",
	}
	if record != expected {
		t.Errorf("NewRecord() = %+v, want %+v", record, expected)
	}
	if err := record.Validate(); err != nil {
		t.Errorf("Record.Validate() error = %v", err)
	}
}

func TestNewRecord_AcceptsEmptyLocation(t *testing.T) {
	t.Parallel()

	record, err := source.NewRecord(
		netip.MustParsePrefix("2001:db8::1/64"),
		"",
		"",
		"primary",
	)
	if err != nil {
		t.Fatalf("NewRecord() error = %v", err)
	}
	if record.Country != "" || record.Subdivision != "" {
		t.Errorf("NewRecord() location = %q/%q, want empty", record.Country, record.Subdivision)
	}
	if err := record.Validate(); err != nil {
		t.Errorf("Record.Validate() error = %v", err)
	}
}

func TestNewRecord_ClassifiesInvalidFields(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		prefix        netip.Prefix
		country       string
		subdivision   string
		sourceID      string
		expectedError error
	}{
		{
			name:          "prefix",
			country:       "US",
			sourceID:      "primary",
			expectedError: source.ErrInvalidPrefix,
		},
		{
			name:          "country",
			prefix:        netip.MustParsePrefix("192.0.2.0/24"),
			country:       "USA",
			sourceID:      "primary",
			expectedError: source.ErrInvalidCountry,
		},
		{
			name:          "subdivision",
			prefix:        netip.MustParsePrefix("192.0.2.0/24"),
			country:       "US",
			subdivision:   "US-CA",
			sourceID:      "primary",
			expectedError: source.ErrInvalidSubdivision,
		},
		{
			name:          "source id",
			prefix:        netip.MustParsePrefix("192.0.2.0/24"),
			country:       "US",
			sourceID:      "",
			expectedError: source.ErrInvalidSourceID,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			_, err := source.NewRecord(
				test.prefix,
				test.country,
				test.subdivision,
				test.sourceID,
			)
			if !errors.Is(err, test.expectedError) {
				t.Fatalf("NewRecord() error = %v, want classification %v", err, test.expectedError)
			}
		})
	}
}

func TestRecord_ValidateRejectsInvalidExternalValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		record        source.Record
		expectedError error
	}{
		{name: "zero value", expectedError: source.ErrInvalidPrefix},
		{
			name: "noncanonical prefix",
			record: source.Record{
				Prefix:   netip.MustParsePrefix("192.0.2.129/24"),
				Country:  "US",
				SourceID: "primary",
			},
			expectedError: source.ErrInvalidPrefix,
		},
		{
			name: "mapped prefix",
			record: source.Record{
				Prefix:   netip.MustParsePrefix("::ffff:192.0.2.0/120"),
				Country:  "US",
				SourceID: "primary",
			},
			expectedError: source.ErrInvalidPrefix,
		},
		{
			name: "lowercase country",
			record: source.Record{
				Prefix:   netip.MustParsePrefix("192.0.2.0/24"),
				Country:  "us",
				SourceID: "primary",
			},
			expectedError: source.ErrInvalidCountry,
		},
		{
			name: "lowercase subdivision",
			record: source.Record{
				Prefix:      netip.MustParsePrefix("192.0.2.0/24"),
				Country:     "US",
				Subdivision: "ca",
				SourceID:    "primary",
			},
			expectedError: source.ErrInvalidSubdivision,
		},
		{
			name: "invalid source id",
			record: source.Record{
				Prefix:   netip.MustParsePrefix("192.0.2.0/24"),
				Country:  "US",
				SourceID: " primary",
			},
			expectedError: source.ErrInvalidSourceID,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			before := test.record
			err := test.record.Validate()
			if !errors.Is(err, test.expectedError) {
				t.Fatalf("Record.Validate() error = %v, want classification %v", err, test.expectedError)
			}
			if test.record != before {
				t.Errorf("Record.Validate() mutated record: got %+v, want %+v", test.record, before)
			}
		})
	}
}

func TestRecord_CopyIsIndependent(t *testing.T) {
	t.Parallel()

	original := mustRecord(t, "192.0.2.0/24", "US", "CA", "primary")
	copyOfRecord := original
	copyOfRecord.Country = "GB"
	copyOfRecord.Subdivision = "ENG"
	copyOfRecord.SourceID = "secondary"

	if original.Country != "US" || original.Subdivision != "CA" || original.SourceID != "primary" {
		t.Errorf("mutating record copy changed original: %+v", original)
	}
}

func TestCompare(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		left  source.Record
		right source.Record
	}{
		{
			name:  "ipv4 before ipv6",
			left:  mustRecord(t, "203.0.113.0/24", "US", "CA", "primary"),
			right: mustRecord(t, "2001:db8::/32", "US", "CA", "primary"),
		},
		{
			name:  "address ascending",
			left:  mustRecord(t, "192.0.2.0/24", "US", "CA", "primary"),
			right: mustRecord(t, "198.51.100.0/24", "US", "CA", "primary"),
		},
		{
			name:  "prefix length ascending",
			left:  mustRecord(t, "192.0.2.0/24", "US", "CA", "primary"),
			right: mustRecord(t, "192.0.2.0/25", "US", "CA", "primary"),
		},
		{
			name:  "source id ascending",
			left:  mustRecord(t, "192.0.2.0/24", "US", "CA", "primary"),
			right: mustRecord(t, "192.0.2.0/24", "US", "CA", "secondary"),
		},
		{
			name:  "country ascending",
			left:  mustRecord(t, "192.0.2.0/24", "GB", "ENG", "primary"),
			right: mustRecord(t, "192.0.2.0/24", "US", "CA", "primary"),
		},
		{
			name:  "subdivision ascending",
			left:  mustRecord(t, "192.0.2.0/24", "US", "CA", "primary"),
			right: mustRecord(t, "192.0.2.0/24", "US", "NY", "primary"),
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			if result := source.Compare(test.left, test.right); result >= 0 {
				t.Errorf("Compare(left, right) = %d, want negative", result)
			}
			if result := source.Compare(test.right, test.left); result <= 0 {
				t.Errorf("Compare(right, left) = %d, want positive", result)
			}
			if result := source.Compare(test.left, test.left); result != 0 {
				t.Errorf("Compare(left, left) = %d, want zero", result)
			}
		})
	}
}

func TestCompare_ShuffledInputsProduceSameOrder(t *testing.T) {
	t.Parallel()

	expected := []source.Record{
		mustRecord(t, "192.0.2.0/24", "GB", "ENG", "primary"),
		mustRecord(t, "192.0.2.0/24", "US", "CA", "primary"),
		mustRecord(t, "192.0.2.0/24", "US", "NY", "primary"),
		mustRecord(t, "192.0.2.0/24", "US", "NY", "secondary"),
		mustRecord(t, "192.0.2.0/25", "US", "CA", "primary"),
		mustRecord(t, "198.51.100.0/24", "US", "CA", "primary"),
		mustRecord(t, "2001:db8::/32", "US", "CA", "primary"),
	}
	shuffles := [][]source.Record{
		{expected[6], expected[2], expected[4], expected[0], expected[5], expected[3], expected[1]},
		{expected[3], expected[5], expected[1], expected[6], expected[0], expected[2], expected[4]},
	}

	for index, records := range shuffles {
		slices.SortFunc(records, source.Compare)
		if !slices.Equal(records, expected) {
			t.Errorf("sorted shuffle %d = %+v, want %+v", index, records, expected)
		}
	}
}

func mustRecord(
	t *testing.T,
	prefix string,
	country string,
	subdivision string,
	sourceID string,
) source.Record {
	t.Helper()

	record, err := source.NewRecord(
		netip.MustParsePrefix(prefix),
		country,
		subdivision,
		sourceID,
	)
	if err != nil {
		t.Fatalf("NewRecord() error = %v", err)
	}

	return record
}
