package maxmind

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/vikewoods/stategeodb/internal/source"
)

func TestDatabaseRecords(t *testing.T) {
	database, err := Open(fixturePath("GeoIP2-City-Test.mmdb"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	metadata := database.Metadata()
	first, err := database.Records(t.Context(), "maxmind-city")
	if err != nil {
		t.Fatalf("Records() error = %v", err)
	}
	second, err := database.Records(t.Context(), "maxmind-city")
	if err != nil {
		t.Fatalf("second Records() error = %v", err)
	}

	if len(first) != 250 {
		t.Fatalf("len(Records()) = %d, want 250", len(first))
	}
	if !slices.Equal(first, second) {
		t.Fatal("repeated Records() calls returned different records")
	}
	if !slices.IsSortedFunc(first, source.Compare) {
		t.Fatal("Records() result is not sorted by source.Compare")
	}

	seenPrefixes := make(map[netip.Prefix]struct{}, len(first))
	var hasIPv4, hasIPv6, hasUnknown bool
	for i, record := range first {
		if err := record.Validate(); err != nil {
			t.Errorf("record %d Validate() error = %v", i, err)
		}
		if record.SourceID != "maxmind-city" {
			t.Errorf("record %d SourceID = %q, want %q", i, record.SourceID, "maxmind-city")
		}
		if record.Prefix.Addr().Is4In6() {
			t.Errorf("record %d has mapped IPv4-in-IPv6 prefix %s", i, record.Prefix)
		}
		if _, exists := seenPrefixes[record.Prefix]; exists {
			t.Errorf("record %d repeats prefix %s", i, record.Prefix)
		}
		seenPrefixes[record.Prefix] = struct{}{}

		hasIPv4 = hasIPv4 || record.Prefix.Addr().Is4()
		hasIPv6 = hasIPv6 || record.Prefix.Addr().Is6()
		hasUnknown = hasUnknown || record.Country == "" && record.Subdivision == ""
	}
	if !hasIPv4 || !hasIPv6 || !hasUnknown {
		t.Errorf("fixture coverage: ipv4=%t ipv6=%t unknown=%t", hasIPv4, hasIPv6, hasUnknown)
	}

	expected := map[netip.Prefix]source.Record{
		netip.MustParsePrefix("2.2.3.0/24"): {
			Prefix:      netip.MustParsePrefix("2.2.3.0/24"),
			Country:     "GB",
			Subdivision: "ENG",
			SourceID:    "maxmind-city",
		},
		netip.MustParsePrefix("2.3.3.0/24"): {
			Prefix:   netip.MustParsePrefix("2.3.3.0/24"),
			SourceID: "maxmind-city",
		},
		netip.MustParsePrefix("175.16.199.0/24"): {
			Prefix:      netip.MustParsePrefix("175.16.199.0/24"),
			Country:     "CN",
			Subdivision: "22",
			SourceID:    "maxmind-city",
		},
		netip.MustParsePrefix("2001:480:10::/48"): {
			Prefix:      netip.MustParsePrefix("2001:480:10::/48"),
			Country:     "US",
			Subdivision: "CA",
			SourceID:    "maxmind-city",
		},
	}
	for _, record := range first {
		want, ok := expected[record.Prefix]
		if !ok {
			continue
		}
		if record != want {
			t.Errorf("record for %s = %+v, want %+v", record.Prefix, record, want)
		}
		delete(expected, record.Prefix)
	}
	if len(expected) != 0 {
		t.Errorf("missing expected fixture records: %v", expected)
	}

	defaultCount := countNetworks(t, database.reader.Networks())
	aliasedCount := countNetworks(
		t,
		database.reader.Networks(maxminddb.IncludeAliasedNetworks()),
	)
	if defaultCount != len(first) {
		t.Errorf("default network count = %d, Records() count = %d", defaultCount, len(first))
	}
	if aliasedCount <= defaultCount {
		t.Errorf("aliased network count = %d, want greater than default %d", aliasedCount, defaultCount)
	}

	assertMetadataEqual(t, database.Metadata(), metadata)
}

func TestDecodeCityRecord(t *testing.T) {
	tests := []struct {
		name         string
		country      *string
		subdivisions []*string
		expected     source.Record
	}{
		{
			name: "missing location",
			expected: source.Record{
				Prefix:   netip.MustParsePrefix("192.0.2.0/24"),
				SourceID: "primary",
			},
		},
		{
			name:         "first subdivision only",
			country:      stringPointer("us"),
			subdivisions: []*string{stringPointer("ca"), stringPointer("nv")},
			expected: source.Record{
				Prefix:      netip.MustParsePrefix("192.0.2.0/24"),
				Country:     "US",
				Subdivision: "CA",
				SourceID:    "primary",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoder := &cityPathDecoder{
				country:      test.country,
				subdivisions: test.subdivisions,
			}
			record, err := decodeCityRecord(
				netip.MustParsePrefix("192.0.2.0/24"),
				"primary",
				decoder.DecodePath,
			)
			if err != nil {
				t.Fatalf("decodeCityRecord() error = %v", err)
			}
			if record != test.expected {
				t.Errorf("decodeCityRecord() = %+v, want %+v", record, test.expected)
			}
			decoder.assertMinimalPaths(t)
		})
	}
}

func TestDecodeCityRecordClassifiesSchemaFailures(t *testing.T) {
	tests := []struct {
		name            string
		country         *string
		subdivisions    []*string
		decodeError     error
		decodeErrorPath []any
		expected        error
	}{
		{
			name:            "incompatible country type",
			decodeError:     errors.New("unsafe decoded value 123"),
			decodeErrorPath: []any{"country", "iso_code"},
			expected:        ErrIngest,
		},
		{
			name:            "incompatible subdivision type",
			country:         stringPointer("US"),
			decodeError:     errors.New("unsafe decoded value 456"),
			decodeErrorPath: []any{"subdivisions", 0, "iso_code"},
			expected:        ErrIngest,
		},
		{
			name:         "subdivision without country",
			subdivisions: []*string{stringPointer("CA")},
			expected:     source.ErrInvalidSubdivision,
		},
		{
			name:     "invalid country",
			country:  stringPointer("USA"),
			expected: source.ErrInvalidCountry,
		},
		{
			name:         "invalid subdivision",
			country:      stringPointer("US"),
			subdivisions: []*string{stringPointer("CALI")},
			expected:     source.ErrInvalidSubdivision,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			decoder := &cityPathDecoder{
				country:         test.country,
				subdivisions:    test.subdivisions,
				decodeError:     test.decodeError,
				decodeErrorPath: test.decodeErrorPath,
			}
			record, err := decodeCityRecord(
				netip.MustParsePrefix("198.51.100.0/24"),
				"primary",
				decoder.DecodePath,
			)
			if record != (source.Record{}) {
				t.Errorf("decodeCityRecord() record = %+v, want zero value", record)
			}
			if !errors.Is(err, test.expected) {
				t.Fatalf("decodeCityRecord() error = %v, want errors.Is(%v)", err, test.expected)
			}
			if test.decodeError != nil && strings.Contains(err.Error(), test.decodeError.Error()) {
				t.Errorf("decodeCityRecord() error = %q, leaked decoder detail", err)
			}
		})
	}
}

func TestValidateSortedUniquePrefixesRejectsNormalizedCollision(t *testing.T) {
	native, err := source.NewRecord(
		netip.MustParsePrefix("192.0.2.0/24"),
		"US",
		"CA",
		"primary",
	)
	if err != nil {
		t.Fatalf("NewRecord(native) error = %v", err)
	}
	mapped, err := source.NewRecord(
		netip.MustParsePrefix("::ffff:192.0.2.0/120"),
		"US",
		"CA",
		"primary",
	)
	if err != nil {
		t.Fatalf("NewRecord(mapped) error = %v", err)
	}

	records := []source.Record{native, mapped}
	slices.SortFunc(records, source.Compare)
	err = validateSortedUniquePrefixes(records)
	if !errors.Is(err, ErrIngest) {
		t.Fatalf("validateSortedUniquePrefixes() error = %v, want errors.Is(ErrIngest)", err)
	}
	if strings.Contains(err.Error(), native.Prefix.String()) {
		t.Errorf("validateSortedUniquePrefixes() error = %q, leaked source prefix", err)
	}
}

func TestDatabaseRecordsValidatesSourceIDBeforeTraversal(t *testing.T) {
	database, err := Open(fixturePath("GeoIP2-City-Test.mmdb"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	records, err := database.Records(t.Context(), "invalid/source")
	if records != nil {
		t.Errorf("Records() = %v, want nil", records)
	}
	if !errors.Is(err, source.ErrInvalidSourceID) {
		t.Fatalf("Records() error = %v, want errors.Is(source.ErrInvalidSourceID)", err)
	}

	if _, err := database.Records(t.Context(), "primary"); err != nil {
		t.Fatalf("Records() after invalid source id error = %v", err)
	}
}

func TestDatabaseRecordsCancellation(t *testing.T) {
	database, err := Open(fixturePath("GeoIP2-City-Test.mmdb"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer database.Close()

	t.Run("already canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		records, err := database.Records(ctx, "invalid/source")
		assertCanceledRecords(t, records, err, context.Canceled)
	})

	t.Run("during iteration", func(t *testing.T) {
		ctx := &cancelAfterChecksContext{
			Context:   t.Context(),
			remaining: 3,
			err:       context.Canceled,
		}

		records, err := database.Records(ctx, "primary")
		assertCanceledRecords(t, records, err, context.Canceled)
	})

	t.Run("after sorting", func(t *testing.T) {
		ctx := &cancelAfterChecksContext{
			Context:   t.Context(),
			remaining: countNetworks(t, database.reader.Networks()) + 2,
			err:       context.Canceled,
		}

		records, err := database.Records(ctx, "primary")
		assertCanceledRecords(t, records, err, context.Canceled)
	})

	t.Run("deadline exceeded", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
		defer cancel()

		records, err := database.Records(ctx, "primary")
		assertCanceledRecords(t, records, err, context.DeadlineExceeded)
	})

	if records, err := database.Records(t.Context(), "primary"); err != nil || len(records) == 0 {
		t.Fatalf("Records() after cancellation = %d records, %v", len(records), err)
	}
}

func TestDatabaseRecordsRejectsUnavailableDatabase(t *testing.T) {
	t.Run("closed", func(t *testing.T) {
		database, err := Open(fixturePath("GeoIP2-City-Test.mmdb"))
		if err != nil {
			t.Fatalf("Open() error = %v", err)
		}
		metadata := database.Metadata()
		if err := database.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}

		records, err := database.Records(t.Context(), "primary")
		if records != nil {
			t.Errorf("Records() = %v, want nil", records)
		}
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Records() error = %v, want errors.Is(ErrUnavailable)", err)
		}
		assertMetadataEqual(t, database.Metadata(), metadata)
	})

	t.Run("nil receiver", func(t *testing.T) {
		var database *Database
		records, err := database.Records(t.Context(), "primary")
		if records != nil {
			t.Errorf("Records() = %v, want nil", records)
		}
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("Records() error = %v, want errors.Is(ErrUnavailable)", err)
		}
	})
}

type cityPathDecoder struct {
	country         *string
	subdivisions    []*string
	decodeError     error
	decodeErrorPath []any
	paths           [][]any
}

func (d *cityPathDecoder) DecodePath(destination any, path ...any) error {
	d.paths = append(d.paths, slices.Clone(path))
	if d.decodeError != nil && pathEqual(path, d.decodeErrorPath...) {
		return d.decodeError
	}

	target, ok := destination.(**string)
	if !ok {
		return errors.New("unexpected destination type")
	}

	switch {
	case pathEqual(path, "country", "iso_code"):
		*target = d.country
	case pathEqual(path, "subdivisions", 0, "iso_code"):
		if len(d.subdivisions) > 0 {
			*target = d.subdivisions[0]
		}
	default:
		return fmt.Errorf("unexpected decode path: %v", path)
	}
	return nil
}

func (d *cityPathDecoder) assertMinimalPaths(t *testing.T) {
	t.Helper()

	if len(d.paths) != 2 {
		t.Fatalf("DecodePath calls = %d, want 2", len(d.paths))
	}
	if !pathEqual(d.paths[0], "country", "iso_code") {
		t.Errorf("country path = %v", d.paths[0])
	}
	if !pathEqual(d.paths[1], "subdivisions", 0, "iso_code") {
		t.Errorf("subdivision path = %v", d.paths[1])
	}
}

type cancelAfterChecksContext struct {
	context.Context
	remaining int
	err       error
}

func (c *cancelAfterChecksContext) Err() error {
	if c.remaining == 0 {
		return c.err
	}
	c.remaining--
	return nil
}

func countNetworks(t *testing.T, networks func(func(maxminddb.Result) bool)) int {
	t.Helper()

	var count int
	for result := range networks {
		if err := result.Err(); err != nil {
			t.Fatalf("Networks() result error = %v", err)
		}
		count++
	}
	return count
}

func assertCanceledRecords(t *testing.T, records []source.Record, err error, target error) {
	t.Helper()

	if records != nil {
		t.Errorf("Records() = %v, want nil", records)
	}
	if !errors.Is(err, target) {
		t.Fatalf("Records() error = %v, want errors.Is(%v)", err, target)
	}
	if errors.Is(err, ErrCorrupt) || errors.Is(err, ErrIngest) {
		t.Errorf("Records() cancellation error = %v, has incorrect database classification", err)
	}
}

func pathEqual(actual []any, expected ...any) bool {
	if len(actual) != len(expected) {
		return false
	}
	for i := range actual {
		if actual[i] != expected[i] {
			return false
		}
	}
	return true
}

func stringPointer(value string) *string {
	return &value
}
