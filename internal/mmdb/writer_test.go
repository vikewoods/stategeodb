package mmdb

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"os/exec"
	"reflect"
	"slices"
	"strings"
	"testing"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/vikewoods/stategeodb/internal/source"
)

const testBuildEpoch int64 = 1_700_000_000

type runtimeRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	Subdivisions []struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"subdivisions"`
}

func TestWriteCompatibility(t *testing.T) {
	if SchemaVersion != 1 {
		t.Fatalf("SchemaVersion = %d, want 1", SchemaVersion)
	}
	records := compatibilityRecords(t)
	data := writeDatabase(t, records, testBuildEpoch)
	database := openDatabase(t, data)

	if err := database.Verify(); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	metadata := database.Metadata
	if metadata.DatabaseType != DatabaseType {
		t.Errorf("DatabaseType = %q, want %q", metadata.DatabaseType, DatabaseType)
	}
	expectedDescription := map[string]string{"en": SchemaDescription}
	if !reflect.DeepEqual(metadata.Description, expectedDescription) {
		t.Errorf("Description = %#v, want %#v", metadata.Description, expectedDescription)
	}
	if metadata.BuildEpoch != uint(testBuildEpoch) {
		t.Errorf("BuildEpoch = %d, want %d", metadata.BuildEpoch, testBuildEpoch)
	}
	if metadata.IPVersion != 6 {
		t.Errorf("IPVersion = %d, want 6", metadata.IPVersion)
	}
	if metadata.RecordSize != 28 {
		t.Errorf("RecordSize = %d, want 28", metadata.RecordSize)
	}
	if metadata.BinaryFormatMajorVersion != 2 || metadata.BinaryFormatMinorVersion != 0 {
		t.Errorf(
			"binary format = %d.%d, want 2.0",
			metadata.BinaryFormatMajorVersion,
			metadata.BinaryFormatMinorVersion,
		)
	}
	if len(metadata.Languages) != 0 {
		t.Errorf("Languages = %v, want empty", metadata.Languages)
	}

	lookups := []struct {
		name                string
		address             string
		expectedCountry     string
		expectedSubdivision string
	}{
		{name: "ipv4 outer overlap", address: "10.2.0.1", expectedCountry: "AA"},
		{name: "ipv4 middle overlap", address: "10.1.3.1", expectedCountry: "BB"},
		{name: "ipv4 longest overlap", address: "10.1.2.1", expectedCountry: "CC"},
		{name: "ipv4 subdivision", address: "192.0.2.1", expectedCountry: "US", expectedSubdivision: "CA"},
		{name: "ipv6 country", address: "2001:db8::1", expectedCountry: "GB"},
		{name: "data-bearing unknown", address: "198.51.100.1"},
	}
	for _, test := range lookups {
		t.Run(test.name, func(t *testing.T) {
			result := database.Lookup(netip.MustParseAddr(test.address))
			if err := result.Err(); err != nil {
				t.Fatalf("Lookup() error = %v", err)
			}
			if !result.Found() {
				t.Fatal("Lookup() Found() = false, want true")
			}

			var actual runtimeRecord
			if err := result.Decode(&actual); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if actual.Country.ISOCode != test.expectedCountry {
				t.Errorf("country = %q, want %q", actual.Country.ISOCode, test.expectedCountry)
			}
			actualSubdivision := ""
			if len(actual.Subdivisions) > 0 {
				actualSubdivision = actual.Subdivisions[0].ISOCode
			}
			if actualSubdivision != test.expectedSubdivision {
				t.Errorf("subdivision = %q, want %q", actualSubdivision, test.expectedSubdivision)
			}
		})
	}

	fullResult := database.Lookup(netip.MustParseAddr("192.0.2.1"))
	var raw map[string]any
	if err := fullResult.Decode(&raw); err != nil {
		t.Fatalf("Decode(raw) error = %v", err)
	}
	expectedRaw := map[string]any{
		"country": map[string]any{
			"iso_code": "US",
		},
		"subdivisions": []any{
			map[string]any{
				"iso_code": "CA",
			},
		},
	}
	if !reflect.DeepEqual(raw, expectedRaw) {
		t.Errorf("decoded schema = %#v, want %#v", raw, expectedRaw)
	}
	countryOnlyResult := database.Lookup(netip.MustParseAddr("2001:db8::1"))
	var countryOnly map[string]any
	if err := countryOnlyResult.Decode(&countryOnly); err != nil {
		t.Fatalf("Decode(country only) error = %v", err)
	}
	expectedCountryOnly := map[string]any{
		"country": map[string]any{
			"iso_code": "GB",
		},
	}
	if !reflect.DeepEqual(countryOnly, expectedCountryOnly) {
		t.Errorf("country-only schema = %#v, want %#v", countryOnly, expectedCountryOnly)
	}
	unknownResult := database.Lookup(netip.MustParseAddr("198.51.100.1"))
	var unknown map[string]any
	if err := unknownResult.Decode(&unknown); err != nil {
		t.Fatalf("Decode(unknown) error = %v", err)
	}
	if !reflect.DeepEqual(unknown, map[string]any{}) {
		t.Errorf("unknown schema = %#v, want data-bearing empty map", unknown)
	}
	for _, path := range []string{"city", "continent", "location", "postal", "source_id", "traits"} {
		var value *any
		if err := fullResult.DecodePath(&value, path); err != nil {
			t.Errorf("DecodePath(%q) error = %v", path, err)
			continue
		}
		if value != nil {
			t.Errorf("DecodePath(%q) = %#v, want missing", path, value)
		}
	}

	firstNetworks := collectNetworks(t, database)
	secondNetworks := collectNetworks(t, database)
	if !slices.Equal(firstNetworks, secondNetworks) {
		t.Errorf("Networks() order changed: first=%v second=%v", firstNetworks, secondNetworks)
	}
	seen := make(map[netip.Prefix]struct{}, len(firstNetworks))
	unknownPrefix := netip.MustParsePrefix("198.51.100.0/24")
	hasUnknown := false
	for _, prefix := range firstNetworks {
		if !prefix.IsValid() || prefix != prefix.Masked() {
			t.Errorf("Networks() returned noncanonical prefix %s", prefix)
		}
		if prefix.Addr().Is4In6() {
			t.Errorf("Networks() returned mapped IPv4 prefix %s", prefix)
		}
		if _, duplicate := seen[prefix]; duplicate {
			t.Errorf("Networks() repeated prefix %s", prefix)
		}
		seen[prefix] = struct{}{}
		if prefix == unknownPrefix {
			hasUnknown = true
		}
	}
	if !hasUnknown {
		t.Errorf("Networks() did not contain data-bearing unknown prefix %s", unknownPrefix)
	}

	aliasedCount := 0
	for result := range database.Networks(maxminddb.IncludeAliasedNetworks()) {
		if err := result.Err(); err != nil {
			t.Fatalf("Networks(IncludeAliasedNetworks) error = %v", err)
		}
		aliasedCount++
	}
	if aliasedCount <= len(firstNetworks) {
		t.Errorf("aliased network count = %d, want greater than default %d", aliasedCount, len(firstNetworks))
	}
}

func TestWriteDeterminism(t *testing.T) {
	records := compatibilityRecords(t)
	original := slices.Clone(records)
	baseline := writeDatabase(t, records, testBuildEpoch)
	repeated := writeDatabase(t, records, testBuildEpoch)
	if !bytes.Equal(baseline, repeated) {
		t.Fatal("identical inputs produced different bytes")
	}
	if !reflect.DeepEqual(records, original) {
		t.Fatalf("Write() mutated records: got %+v want %+v", records, original)
	}

	shuffled := slices.Clone(records)
	slices.Reverse(shuffled)
	if actual := writeDatabase(t, shuffled, testBuildEpoch); !bytes.Equal(actual, baseline) {
		t.Fatal("shuffled input produced different bytes")
	}

	changedSourceIDs := slices.Clone(records)
	for index := range changedSourceIDs {
		changedSourceIDs[index].SourceID = fmt.Sprintf("replacement-%d", index)
	}
	if actual := writeDatabase(t, changedSourceIDs, testBuildEpoch); !bytes.Equal(actual, baseline) {
		t.Fatal("source ids affected encoded bytes")
	}

	differentEpoch := writeDatabase(t, records, testBuildEpoch+1)
	if bytes.Equal(differentEpoch, baseline) {
		t.Fatal("different build epochs produced identical bytes")
	}
	database := openDatabase(t, differentEpoch)
	if database.Metadata.BuildEpoch != uint(testBuildEpoch+1) {
		t.Errorf("BuildEpoch = %d, want %d", database.Metadata.BuildEpoch, testBuildEpoch+1)
	}
	var decoded runtimeRecord
	if err := database.Lookup(netip.MustParseAddr("192.0.2.1")).Decode(&decoded); err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded.Country.ISOCode != "US" || len(decoded.Subdivisions) != 1 || decoded.Subdivisions[0].ISOCode != "CA" {
		t.Errorf("lookup after epoch change = %+v", decoded)
	}
}

func TestWriteCrossProcessDeterminism(t *testing.T) {
	const processCount = 3
	var expected string
	for range processCount {
		command := exec.Command(os.Args[0], "-test.run=^TestWriteDeterminismHelper$")
		command.Env = append(os.Environ(), "STATEGEO_MMDB_DETERMINISM_HELPER=1")
		output, err := command.Output()
		if err != nil {
			t.Fatalf("helper process error = %v", err)
		}
		digest := digestFromOutput(t, string(output))
		if expected == "" {
			expected = digest
			continue
		}
		if digest != expected {
			t.Fatalf("helper digest = %q, want %q", digest, expected)
		}
	}
}

func TestWriteDeterminismHelper(t *testing.T) {
	if os.Getenv("STATEGEO_MMDB_DETERMINISM_HELPER") != "1" {
		t.Skip("subprocess helper")
	}
	data := writeDatabase(t, compatibilityRecords(t), testBuildEpoch)
	digest := sha256.Sum256(data)
	fmt.Printf("stategeo-digest=%x\n", digest)
}

func TestWriteRejectsInvalidInput(t *testing.T) {
	valid := mustRecord(t, "192.0.2.0/24", "US", "CA", "valid")
	var typedNil *bytes.Buffer
	tests := []struct {
		name          string
		destination   io.Writer
		records       []source.Record
		epoch         int64
		expectedCause error
	}{
		{name: "nil destination", records: []source.Record{valid}, epoch: testBuildEpoch},
		{name: "typed nil destination", destination: typedNil, records: []source.Record{valid}, epoch: testBuildEpoch},
		{name: "empty records", destination: &bytes.Buffer{}, records: []source.Record{}, epoch: testBuildEpoch},
		{name: "zero epoch", destination: &bytes.Buffer{}, records: []source.Record{valid}},
		{name: "negative epoch", destination: &bytes.Buffer{}, records: []source.Record{valid}, epoch: -1},
		{
			name:          "invalid prefix",
			destination:   &bytes.Buffer{},
			records:       []source.Record{{Country: "US", SourceID: "invalid"}},
			epoch:         testBuildEpoch,
			expectedCause: source.ErrInvalidPrefix,
		},
		{
			name:          "invalid country",
			destination:   &bytes.Buffer{},
			records:       []source.Record{{Prefix: valid.Prefix, Country: "us", SourceID: "invalid"}},
			epoch:         testBuildEpoch,
			expectedCause: source.ErrInvalidCountry,
		},
		{
			name:          "invalid subdivision",
			destination:   &bytes.Buffer{},
			records:       []source.Record{{Prefix: valid.Prefix, Subdivision: "CA", SourceID: "invalid"}},
			epoch:         testBuildEpoch,
			expectedCause: source.ErrInvalidSubdivision,
		},
		{
			name:          "invalid source id",
			destination:   &bytes.Buffer{},
			records:       []source.Record{{Prefix: valid.Prefix, Country: "US"}},
			epoch:         testBuildEpoch,
			expectedCause: source.ErrInvalidSourceID,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			before := slices.Clone(test.records)
			written, err := Write(test.destination, test.records, Options{BuildEpoch: test.epoch})
			if written != 0 {
				t.Errorf("Write() bytes = %d, want 0", written)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Write() error = %v, want errors.Is(ErrInvalidInput)", err)
			}
			if test.expectedCause != nil && !errors.Is(err, test.expectedCause) {
				t.Errorf("Write() error = %v, want errors.Is(%v)", err, test.expectedCause)
			}
			if !reflect.DeepEqual(test.records, before) {
				t.Errorf("Write() mutated invalid records: got %+v want %+v", test.records, before)
			}
		})
	}
}

func TestWriteRejectsDuplicatePrefixes(t *testing.T) {
	first := mustRecord(t, "192.0.2.0/24", "US", "CA", "first")
	second := mustRecord(t, "192.0.2.0/24", "GB", "ENG", "second")
	records := []source.Record{second, first}
	before := slices.Clone(records)
	written, err := Write(&bytes.Buffer{}, records, Options{BuildEpoch: testBuildEpoch})
	if written != 0 {
		t.Errorf("Write() bytes = %d, want 0", written)
	}
	if !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("Write() error = %v, want errors.Is(ErrInvalidInput)", err)
	}
	if !reflect.DeepEqual(records, before) {
		t.Errorf("Write() mutated records: got %+v want %+v", records, before)
	}
}

func TestWriteClassifiesInsertionFailure(t *testing.T) {
	record := mustRecord(t, "2002::/16", "US", "", "alias-conflict")
	written, err := Write(&bytes.Buffer{}, []source.Record{record}, Options{BuildEpoch: testBuildEpoch})
	if written != 0 {
		t.Errorf("Write() bytes = %d, want 0", written)
	}
	if !errors.Is(err, ErrBuild) {
		t.Fatalf("Write() error = %v, want errors.Is(ErrBuild)", err)
	}
	if strings.Contains(err.Error(), record.Prefix.String()) {
		t.Errorf("Write() error leaked input prefix: %q", err)
	}
}

func TestWriteSeparatesIPv4FromBroadIPv6(t *testing.T) {
	records := []source.Record{
		mustRecord(t, "::/0", "ZZ", "", "ipv6-default"),
		mustRecord(t, "192.0.2.0/24", "US", "CA", "ipv4-known"),
		mustRecord(t, "198.51.100.0/24", "", "", "ipv4-unknown"),
	}
	database := openDatabase(t, writeDatabase(t, records, testBuildEpoch))
	if err := database.Verify(); err != nil {
		t.Fatalf("Verify() error = %v", err)
	}

	lookups := []struct {
		name            string
		address         string
		expectedFound   bool
		expectedCountry string
	}{
		{name: "native ipv6", address: "3000::1", expectedFound: true, expectedCountry: "ZZ"},
		{name: "specific ipv4", address: "192.0.2.1", expectedFound: true, expectedCountry: "US"},
		{name: "unknown ipv4", address: "198.51.100.1", expectedFound: true},
		{name: "uncovered ipv4", address: "203.0.113.1"},
		{name: "ipv4 alias", address: "2002:cb00:7101::"},
	}
	for _, test := range lookups {
		t.Run(test.name, func(t *testing.T) {
			result := database.Lookup(netip.MustParseAddr(test.address))
			if err := result.Err(); err != nil {
				t.Fatalf("Lookup() error = %v", err)
			}
			if result.Found() != test.expectedFound {
				t.Fatalf("Found() = %t, want %t", result.Found(), test.expectedFound)
			}
			var record runtimeRecord
			if err := result.Decode(&record); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if record.Country.ISOCode != test.expectedCountry {
				t.Errorf("country = %q, want %q", record.Country.ISOCode, test.expectedCountry)
			}
		})
	}
}

func TestWriteRejectsNativeIPv6InIPv4Storage(t *testing.T) {
	for _, prefix := range []string{"::/96", "::1/128"} {
		t.Run(prefix, func(t *testing.T) {
			record := mustRecord(t, prefix, "ZZ", "", "native-ipv6")
			written, err := Write(&bytes.Buffer{}, []source.Record{record}, Options{BuildEpoch: testBuildEpoch})
			if written != 0 {
				t.Errorf("Write() bytes = %d, want 0", written)
			}
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("Write() error = %v, want errors.Is(ErrInvalidInput)", err)
			}
			if strings.Contains(err.Error(), prefix) {
				t.Errorf("Write() error leaked input prefix: %q", err)
			}
		})
	}
}

func TestWriteClassifiesDestinationFailure(t *testing.T) {
	unsafeCause := errors.New("unsafe destination detail")
	destination := failingWriter{err: unsafeCause}
	record := mustRecord(t, "192.0.2.0/24", "US", "CA", "valid")
	written, err := Write(destination, []source.Record{record}, Options{BuildEpoch: testBuildEpoch})
	if written != 0 {
		t.Errorf("Write() bytes = %d, want 0", written)
	}
	if !errors.Is(err, ErrWrite) {
		t.Fatalf("Write() error = %v, want errors.Is(ErrWrite)", err)
	}
	if errors.Is(err, unsafeCause) || strings.Contains(err.Error(), unsafeCause.Error()) {
		t.Errorf("Write() error exposed destination cause: %v", err)
	}
}

func TestWriteDoesNotCloseDestination(t *testing.T) {
	destination := &closeTrackingWriter{}
	record := mustRecord(t, "192.0.2.0/24", "US", "CA", "valid")
	if _, err := Write(destination, []source.Record{record}, Options{BuildEpoch: testBuildEpoch}); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if destination.isClosed {
		t.Error("Write() closed destination")
	}
}

func TestPrefixNetworkUsesNativeAddressWidths(t *testing.T) {
	tests := []struct {
		name           string
		prefix         string
		expectedIPLen  int
		expectedMask   int
		expectedIPBits int
	}{
		{name: "ipv4", prefix: "192.0.2.0/24", expectedIPLen: 4, expectedMask: 24, expectedIPBits: 32},
		{name: "ipv6", prefix: "2001:db8::/32", expectedIPLen: 16, expectedMask: 32, expectedIPBits: 128},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			network := prefixNetwork(netip.MustParsePrefix(test.prefix))
			if len(network.IP) != test.expectedIPLen {
				t.Errorf("IP length = %d, want %d", len(network.IP), test.expectedIPLen)
			}
			ones, bits := network.Mask.Size()
			if ones != test.expectedMask || bits != test.expectedIPBits {
				t.Errorf("mask = %d/%d, want %d/%d", ones, bits, test.expectedMask, test.expectedIPBits)
			}
		})
	}
}

func compatibilityRecords(t *testing.T) []source.Record {
	t.Helper()
	return []source.Record{
		mustRecord(t, "10.0.0.0/8", "AA", "", "outer"),
		mustRecord(t, "10.1.0.0/16", "BB", "", "middle"),
		mustRecord(t, "10.1.2.0/24", "CC", "", "inner"),
		mustRecord(t, "192.0.2.0/24", "US", "CA", "subdivision"),
		mustRecord(t, "2001:db8::/32", "GB", "", "ipv6"),
		mustRecord(t, "198.51.100.0/24", "", "", "unknown"),
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
	record, err := source.NewRecord(netip.MustParsePrefix(prefix), country, subdivision, sourceID)
	if err != nil {
		t.Fatalf("NewRecord() error = %v", err)
	}
	return record
}

func writeDatabase(t *testing.T, records []source.Record, epoch int64) []byte {
	t.Helper()
	var destination bytes.Buffer
	written, err := Write(&destination, records, Options{BuildEpoch: epoch})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if written != int64(destination.Len()) {
		t.Fatalf("Write() bytes = %d, destination length = %d", written, destination.Len())
	}
	return slices.Clone(destination.Bytes())
}

func openDatabase(t *testing.T, data []byte) *maxminddb.Reader {
	t.Helper()
	database, err := maxminddb.OpenBytes(data)
	if err != nil {
		t.Fatalf("OpenBytes() error = %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return database
}

func collectNetworks(t *testing.T, database *maxminddb.Reader) []netip.Prefix {
	t.Helper()
	prefixes := []netip.Prefix{}
	for result := range database.Networks() {
		if err := result.Err(); err != nil {
			t.Fatalf("Networks() error = %v", err)
		}
		prefixes = append(prefixes, result.Prefix())
	}
	return prefixes
}

func digestFromOutput(t *testing.T, output string) string {
	t.Helper()
	for line := range strings.SplitSeq(output, "\n") {
		if digest, found := strings.CutPrefix(line, "stategeo-digest="); found {
			return digest
		}
	}
	t.Fatalf("helper output did not contain digest: %q", output)
	return ""
}

type failingWriter struct {
	err error
}

func (writer failingWriter) Write(_ []byte) (int, error) {
	return 0, writer.err
}

type closeTrackingWriter struct {
	bytes.Buffer
	isClosed bool
}

func (writer *closeTrackingWriter) Close() error {
	writer.isClosed = true
	return nil
}
