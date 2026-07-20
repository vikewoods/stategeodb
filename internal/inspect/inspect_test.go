package inspect

import (
	"bytes"
	"context"
	"errors"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/vikewoods/stategeodb/internal/mmdb"
	"github.com/vikewoods/stategeodb/internal/source"
)

const (
	testBuildEpoch   int64 = 1_700_000_123
	legacyRecordSize       = 28
)

func TestInspect_MetadataAndBoundedLookups(t *testing.T) {
	path := writeProjectDatabase(t)
	addresses := []netip.Addr{
		netip.MustParseAddr("192.0.2.1"),
		netip.MustParseAddr("2001:db8::1"),
		netip.MustParseAddr("198.51.100.1"),
		netip.MustParseAddr("203.0.113.1"),
		netip.MustParseAddr("::ffff:192.0.2.2"),
	}

	result, err := Inspect(t.Context(), Request{DatabasePath: path, Addresses: addresses})
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	expectedMetadata := Metadata{
		DatabaseType:      mmdb.DatabaseType,
		SchemaVersion:     mmdb.SchemaVersion,
		BuildEpoch:        uint(testBuildEpoch),
		BinaryFormatMajor: 2,
		BinaryFormatMinor: 0,
		IPVersion:         6,
		RecordSize:        mmdb.RecordSize,
	}
	expectedMetadata.NodeCount = result.Metadata.NodeCount
	if result.Metadata.NodeCount == 0 || result.Metadata != expectedMetadata {
		t.Errorf("Metadata = %+v, want %+v with positive node count", result.Metadata, expectedMetadata)
	}
	expectedLookups := []Lookup{
		{Address: netip.MustParseAddr("192.0.2.1"), Found: true, Prefix: netip.MustParsePrefix("192.0.2.0/24"), Country: "US", Subdivision: "CA"},
		{Address: netip.MustParseAddr("2001:db8::1"), Found: true, Prefix: netip.MustParsePrefix("2001:db8::/32"), Country: "GB"},
		{Address: netip.MustParseAddr("198.51.100.1"), Found: true, Prefix: netip.MustParsePrefix("198.51.100.0/24")},
		{Address: netip.MustParseAddr("203.0.113.1")},
		{Address: netip.MustParseAddr("192.0.2.2"), Found: true, Prefix: netip.MustParsePrefix("192.0.2.0/24"), Country: "US", Subdivision: "CA"},
	}
	if !reflect.DeepEqual(result.Lookups, expectedLookups) {
		t.Errorf("Lookups = %#v, want %#v", result.Lookups, expectedLookups)
	}

	addresses[0] = netip.Addr{}
	if result.Lookups[0].Address != netip.MustParseAddr("192.0.2.1") {
		t.Error("result lookup storage changed with caller request storage")
	}
}

func TestInspect_MetadataOnly(t *testing.T) {
	result, err := Inspect(t.Context(), Request{DatabasePath: writeProjectDatabase(t)})
	if err != nil {
		t.Fatalf("Inspect() error = %v", err)
	}
	if result.Lookups == nil || len(result.Lookups) != 0 {
		t.Errorf("Lookups = %#v, want non-nil empty caller-owned slice", result.Lookups)
	}
}

func TestInspect_RejectsLegacyRecordSize(t *testing.T) {
	path := writeCustomDatabase(
		t,
		mmdb.DatabaseType,
		mmdb.SchemaDescription,
		"US",
		legacyRecordSize,
	)
	result, err := Inspect(t.Context(), Request{DatabasePath: path})
	if !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrCorrupt) {
		t.Errorf("Inspect() result/error = %+v/%v, want zero/only ErrUnsupported", result, err)
	}
}

func TestInspect_RejectsInvalidRequestsBeforeOpen(t *testing.T) {
	tests := []Request{
		{},
		{DatabasePath: "database.mmdb", Addresses: []netip.Addr{{}}},
		{DatabasePath: "database.mmdb", Addresses: []netip.Addr{netip.MustParseAddr("fe80::1%eth0")}},
		{DatabasePath: "database.mmdb", Addresses: make([]netip.Addr, maxAddresses+1)},
	}
	for _, request := range tests {
		openCalled := false
		_, err := inspect(t.Context(), request, operations{open: func(string) (database, error) {
			openCalled = true
			return nil, errors.New("unexpected open")
		}})
		if !errors.Is(err, ErrInvalidRequest) || openCalled {
			t.Errorf("inspect() error/open = %v/%t, want ErrInvalidRequest/false", err, openCalled)
		}
	}
	if _, err := Inspect(nil, Request{DatabasePath: "database.mmdb"}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Inspect(nil) error = %v, want ErrInvalidRequest", err)
	}
}

func TestInspect_CancellationBoundaries(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := Inspect(ctx, Request{DatabasePath: "not-opened.mmdb"})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("Inspect(cancelled) error = %v, want context.Canceled", err)
	}

	ctx, cancel = context.WithCancel(t.Context())
	opened := newFakeDatabase()
	opened.verify = func() error {
		cancel()
		return nil
	}
	_, err = inspect(ctx, Request{DatabasePath: "database.mmdb"}, fakeOperations(opened))
	if !errors.Is(err, context.Canceled) || opened.lookupCalls != 0 || opened.closeCalls != 1 {
		t.Errorf("post-verify cancellation = %v, lookups %d, closes %d", err, opened.lookupCalls, opened.closeCalls)
	}

	ctx, cancel = context.WithCancel(t.Context())
	_, err = inspect(ctx, Request{DatabasePath: "private.mmdb"}, operations{
		open: func(path string) (database, error) {
			cancel()
			return nil, &os.PathError{Op: "open", Path: path, Err: os.ErrNotExist}
		},
	})
	if !errors.Is(err, context.Canceled) || !errors.Is(err, ErrOpen) || strings.Contains(err.Error(), "private.mmdb") {
		t.Errorf("during-open cancellation = %v, want safe context.Canceled and ErrOpen", err)
	}
}

func TestInspect_RejectsUnsupportedMetadata(t *testing.T) {
	tests := []struct {
		name         string
		databaseType string
		description  string
	}{
		{name: "source city", databaseType: "GeoLite2-City", description: "GeoLite2 City database"},
		{name: "project type", databaseType: "StateGeo-Unknown", description: mmdb.SchemaDescription},
		{name: "schema", databaseType: mmdb.DatabaseType, description: "stategeodb incompatible schema"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeCustomDatabase(
				t,
				test.databaseType,
				test.description,
				"US",
				mmdb.RecordSize,
			)
			_, err := Inspect(t.Context(), Request{DatabasePath: path})
			if !errors.Is(err, ErrUnsupported) || errors.Is(err, ErrCorrupt) {
				t.Errorf("Inspect() error = %v, want only ErrUnsupported", err)
			}
		})
	}
}

func TestInspect_ClassifiesOpenAndCorruptFailuresWithoutDetails(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "private-source.mmdb")
	_, err := Inspect(t.Context(), Request{DatabasePath: missing})
	if !errors.Is(err, ErrOpen) || strings.Contains(err.Error(), missing) {
		t.Errorf("missing error = %v, want safe ErrOpen", err)
	}
	directory := t.TempDir()
	_, err = Inspect(t.Context(), Request{DatabasePath: directory})
	if !errors.Is(err, ErrOpen) || strings.Contains(err.Error(), directory) {
		t.Errorf("directory error = %v, want safe ErrOpen", err)
	}

	for _, contents := range [][]byte{[]byte("not an mmdb"), truncatedProjectDatabase(t)} {
		path := filepath.Join(t.TempDir(), "corrupt.mmdb")
		if writeErr := os.WriteFile(path, contents, 0o600); writeErr != nil {
			t.Fatalf("WriteFile() error = %v", writeErr)
		}
		_, err = Inspect(t.Context(), Request{DatabasePath: path})
		if !errors.Is(err, ErrCorrupt) || strings.Contains(err.Error(), path) {
			t.Errorf("corrupt error = %v, want safe ErrCorrupt", err)
		}
	}
}

func TestInspect_PreservesMalformedLocationClassification(t *testing.T) {
	path := writeCustomDatabase(
		t,
		mmdb.DatabaseType,
		mmdb.SchemaDescription,
		"USA",
		mmdb.RecordSize,
	)
	_, err := Inspect(t.Context(), Request{
		DatabasePath: path,
		Addresses:    []netip.Addr{netip.MustParseAddr("192.0.2.1")},
	})
	if !errors.Is(err, ErrLookup) || !errors.Is(err, source.ErrInvalidCountry) {
		t.Errorf("Inspect() error = %v, want ErrLookup and ErrInvalidCountry", err)
	}
}

func TestInspect_ClosesAndReturnsNoPartialResultOnFailures(t *testing.T) {
	tests := []struct {
		name          string
		configure     func(*fakeDatabase)
		expectedError error
	}{
		{name: "verify", configure: func(database *fakeDatabase) { database.verifyErr = errors.New("offset 123") }, expectedError: ErrCorrupt},
		{name: "lookup", configure: func(database *fakeDatabase) { database.lookup.err = errors.New("decode secret") }, expectedError: ErrLookup},
		{name: "close", configure: func(database *fakeDatabase) { database.closeErr = errors.New("close secret") }, expectedError: ErrClose},
		{name: "lookup and close", configure: func(database *fakeDatabase) {
			database.lookup.err = errors.New("lookup secret")
			database.closeErr = errors.New("close secret")
		}, expectedError: ErrLookup},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			database := newFakeDatabase()
			test.configure(database)
			result, err := inspect(t.Context(), Request{
				DatabasePath: "private.mmdb",
				Addresses:    []netip.Addr{netip.MustParseAddr("192.0.2.1")},
			}, fakeOperations(database))
			if !errors.Is(err, test.expectedError) || database.closeCalls != 1 || !reflect.DeepEqual(result, Result{}) {
				t.Errorf("inspect() = %#v/%v, closes %d", result, err, database.closeCalls)
			}
			if test.name == "lookup and close" && !errors.Is(err, ErrClose) {
				t.Errorf("joined error = %v, want ErrClose", err)
			}
			for _, secret := range []string{"private.mmdb", "offset 123", "decode secret", "close secret", "lookup secret"} {
				if err != nil && strings.Contains(err.Error(), secret) {
					t.Errorf("error leaked %q: %v", secret, err)
				}
			}
		})
	}
}

func TestInspect_ClosesOnSuccess(t *testing.T) {
	database := newFakeDatabase()
	result, err := inspect(t.Context(), Request{
		DatabasePath: "database.mmdb",
		Addresses:    []netip.Addr{netip.MustParseAddr("192.0.2.1")},
	}, fakeOperations(database))
	if err != nil || database.closeCalls != 1 || len(result.Lookups) != 1 {
		t.Errorf("inspect() = %#v/%v, closes %d", result, err, database.closeCalls)
	}
}

func writeProjectDatabase(t *testing.T) string {
	t.Helper()
	records := []source.Record{
		mustRecord(t, "192.0.2.0/24", "US", "CA"),
		mustRecord(t, "198.51.100.0/24", "", ""),
		mustRecord(t, "2001:db8::/32", "GB", ""),
	}
	path := filepath.Join(t.TempDir(), "project.mmdb")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := mmdb.Write(file, records, mmdb.Options{BuildEpoch: testBuildEpoch}); err != nil {
		_ = file.Close()
		t.Fatalf("Write() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return path
}

func mustRecord(t *testing.T, prefix, country, subdivision string) source.Record {
	t.Helper()
	record, err := source.NewRecord(netip.MustParsePrefix(prefix), country, subdivision, "test")
	if err != nil {
		t.Fatalf("NewRecord() error = %v", err)
	}
	return record
}

func writeCustomDatabase(
	t *testing.T,
	databaseType string,
	description string,
	country string,
	recordSize int,
) string {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch:              testBuildEpoch,
		DatabaseType:            databaseType,
		Description:             map[string]string{"en": description},
		IncludeReservedNetworks: true,
		IPVersion:               6,
		Languages:               []string{},
		RecordSize:              recordSize,
	})
	if err != nil {
		t.Fatalf("mmdbwriter.New() error = %v", err)
	}
	value := mmdbtype.Map{"country": mmdbtype.Map{"iso_code": mmdbtype.String(country)}}
	if err := tree.Insert(prefixNetwork(netip.MustParsePrefix("192.0.2.0/24")), value); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	path := filepath.Join(t.TempDir(), "custom.mmdb")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := tree.WriteTo(file); err != nil {
		_ = file.Close()
		t.Fatalf("WriteTo() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return path
}

func prefixNetwork(prefix netip.Prefix) *net.IPNet {
	if prefix.Addr().Is4() {
		address := prefix.Addr().As4()
		return &net.IPNet{IP: net.IP(address[:]), Mask: net.CIDRMask(prefix.Bits(), 32)}
	}
	address := prefix.Addr().As16()
	return &net.IPNet{IP: net.IP(address[:]), Mask: net.CIDRMask(prefix.Bits(), 128)}
}

func truncatedProjectDatabase(t *testing.T) []byte {
	t.Helper()
	contents, err := os.ReadFile(writeProjectDatabase(t))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	return bytes.Clone(contents[:len(contents)/2])
}

type fakeDatabase struct {
	metadata    maxminddb.Metadata
	verify      func() error
	verifyErr   error
	lookup      fakeLookup
	lookupCalls int
	closeErr    error
	closeCalls  int
}

func newFakeDatabase() *fakeDatabase {
	return &fakeDatabase{
		metadata: maxminddb.Metadata{
			Description:              map[string]string{"en": mmdb.SchemaDescription},
			DatabaseType:             mmdb.DatabaseType,
			Languages:                []string{},
			BinaryFormatMajorVersion: 2,
			BuildEpoch:               uint(testBuildEpoch),
			IPVersion:                6,
			NodeCount:                1,
			RecordSize:               mmdb.RecordSize,
		},
		lookup: fakeLookup{found: true, prefix: netip.MustParsePrefix("192.0.2.0/24"), country: "US"},
	}
}

func (database *fakeDatabase) Metadata() maxminddb.Metadata { return database.metadata }
func (database *fakeDatabase) Verify() error {
	if database.verify != nil {
		return database.verify()
	}
	return database.verifyErr
}
func (database *fakeDatabase) Lookup(netip.Addr) lookupResult {
	database.lookupCalls++
	return database.lookup
}
func (database *fakeDatabase) Close() error {
	database.closeCalls++
	return database.closeErr
}

type fakeLookup struct {
	err         error
	found       bool
	prefix      netip.Prefix
	country     string
	subdivision string
}

func (lookup fakeLookup) Err() error           { return lookup.err }
func (lookup fakeLookup) Found() bool          { return lookup.found }
func (lookup fakeLookup) Prefix() netip.Prefix { return lookup.prefix }
func (lookup fakeLookup) DecodePath(destination any, path ...any) error {
	if lookup.err != nil {
		return lookup.err
	}
	value := lookup.country
	if len(path) > 0 && path[0] == "subdivisions" {
		value = lookup.subdivision
	}
	if value != "" {
		*destination.(**string) = &value
	}
	return nil
}

func fakeOperations(opened database) operations {
	return operations{open: func(string) (database, error) { return opened, nil }}
}
