package maxmind

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

func TestOpen(t *testing.T) {
	database, err := Open(fixturePath("GeoIP2-City-Test.mmdb"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}

	expected := Metadata{
		DatabaseType:             "GeoIP2-City",
		BinaryFormatMajorVersion: 2,
		BinaryFormatMinorVersion: 0,
		BuildEpoch:               1770245369,
		IPVersion:                6,
		NodeCount:                1547,
		RecordSize:               28,
		Languages:                []string{"en", "zh"},
		Description: map[string]string{
			"en": "GeoIP2 City Test Database (fake GeoIP2 data, for example purposes only)",
			"zh": "小型数据库",
		},
	}
	assertMetadataEqual(t, database.Metadata(), expected)

	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}

	assertMetadataEqual(t, database.Metadata(), expected)
}

func TestOpenVerifiesBeforeReturn(t *testing.T) {
	verifyCalled := false
	database, err := openWithOperations(
		fixturePath("GeoIP2-City-Test.mmdb"),
		readerOperations{
			open: maxminddb.Open,
			verify: func(reader *maxminddb.Reader) error {
				verifyCalled = true
				return reader.Verify()
			},
			close: (*maxminddb.Reader).Close,
		},
	)
	if err != nil {
		t.Fatalf("openWithOperations() error = %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	if !verifyCalled {
		t.Fatal("openWithOperations() returned before verification")
	}
}

func TestMetadataReturnsDefensiveCopies(t *testing.T) {
	database, err := Open(fixturePath("GeoIP2-City-Test.mmdb"))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	first := database.Metadata()
	originalLanguage := first.Languages[0]
	originalDescription := first.Description["en"]
	first.Languages[0] = "changed"
	first.Description["en"] = "changed"
	first.Description["new"] = "changed"

	second := database.Metadata()
	if second.Languages[0] != originalLanguage {
		t.Errorf("Metadata().Languages[0] = %q, want %q", second.Languages[0], originalLanguage)
	}
	if second.Description["en"] != originalDescription {
		t.Errorf("Metadata().Description[en] = %q, want %q", second.Description["en"], originalDescription)
	}
	if _, ok := second.Description["new"]; ok {
		t.Error("Metadata().Description retained caller mutation")
	}
}

func TestOpenRejectsUnsupportedDatabaseBeforeVerification(t *testing.T) {
	verifyCalled := false
	closeCalls := 0

	database, err := openWithOperations(
		fixturePath("GeoIP2-Country-Test.mmdb"),
		readerOperations{
			open: maxminddb.Open,
			verify: func(reader *maxminddb.Reader) error {
				verifyCalled = true
				return reader.Verify()
			},
			close: func(reader *maxminddb.Reader) error {
				closeCalls++
				return reader.Close()
			},
		},
	)
	if database != nil {
		t.Fatal("openWithOperations() returned a database for unsupported metadata")
	}
	if !errors.Is(err, ErrUnsupported) {
		t.Fatalf("openWithOperations() error = %v, want errors.Is(ErrUnsupported)", err)
	}
	if errors.Is(err, ErrCorrupt) {
		t.Fatalf("openWithOperations() error = %v, unexpectedly classified as corrupt", err)
	}
	if verifyCalled {
		t.Error("unsupported metadata reached structural verification")
	}
	if closeCalls != 1 {
		t.Errorf("close calls = %d, want 1", closeCalls)
	}
}

func TestOpenPreservesVerificationAndCleanupClassifications(t *testing.T) {
	verifyFailure := errors.New("injected verification failure")
	closeFailure := errors.New("injected close failure")
	closeCalls := 0

	database, err := openWithOperations(
		fixturePath("GeoIP2-City-Test.mmdb"),
		readerOperations{
			open: maxminddb.Open,
			verify: func(*maxminddb.Reader) error {
				return verifyFailure
			},
			close: func(reader *maxminddb.Reader) error {
				closeCalls++
				return errors.Join(reader.Close(), closeFailure)
			},
		},
	)
	if database != nil {
		t.Fatal("openWithOperations() returned a database after verification failure")
	}
	for _, target := range []error{ErrCorrupt, ErrClose} {
		if !errors.Is(err, target) {
			t.Errorf("openWithOperations() error = %v, want errors.Is(%v)", err, target)
		}
	}
	for _, unsafeCause := range []error{verifyFailure, closeFailure} {
		if errors.Is(err, unsafeCause) {
			t.Errorf("openWithOperations() error exposes unsafe cause %v", unsafeCause)
		}
	}
	if closeCalls != 1 {
		t.Errorf("close calls = %d, want 1", closeCalls)
	}
}

func TestDatabaseCloseClassifiesFailure(t *testing.T) {
	closeFailure := errors.New("unsafe close detail")
	closeCalls := 0
	database := &Database{
		reader: &maxminddb.Reader{},
		closeReader: func(*maxminddb.Reader) error {
			closeCalls++
			return closeFailure
		},
	}

	err := database.Close()
	if !errors.Is(err, ErrClose) {
		t.Fatalf("Close() error = %v, want errors.Is(ErrClose)", err)
	}
	if errors.Is(err, closeFailure) {
		t.Fatalf("Close() error = %v, exposed unsafe cause", err)
	}
	if strings.Contains(err.Error(), closeFailure.Error()) {
		t.Fatalf("Close() error = %q, leaked underlying detail", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("second Close() error = %v", err)
	}
	if closeCalls != 1 {
		t.Errorf("close calls = %d, want 1", closeCalls)
	}
}

func TestValidateMetadata(t *testing.T) {
	validMetadata := func(databaseType string) Metadata {
		return Metadata{
			DatabaseType:             databaseType,
			BinaryFormatMajorVersion: 2,
			BinaryFormatMinorVersion: 0,
			BuildEpoch:               1,
			IPVersion:                6,
			NodeCount:                1,
			RecordSize:               28,
			Languages:                []string{"en"},
			Description:              map[string]string{"en": "test database"},
		}
	}

	for _, databaseType := range []string{"GeoLite2-City", "GeoIP2-City"} {
		t.Run("accepts "+databaseType, func(t *testing.T) {
			if err := validateMetadata(validMetadata(databaseType)); err != nil {
				t.Fatalf("validateMetadata() error = %v", err)
			}
		})
	}

	t.Run("accepts ipv4 only", func(t *testing.T) {
		metadata := validMetadata("GeoIP2-City")
		metadata.IPVersion = 4
		if err := validateMetadata(metadata); err != nil {
			t.Fatalf("validateMetadata() error = %v", err)
		}
	})

	tests := []struct {
		name   string
		mutate func(*Metadata)
	}{
		{name: "empty database type", mutate: func(m *Metadata) { m.DatabaseType = "" }},
		{name: "country database", mutate: func(m *Metadata) { m.DatabaseType = "GeoIP2-Country" }},
		{name: "enterprise database", mutate: func(m *Metadata) { m.DatabaseType = "GeoIP2-Enterprise" }},
		{name: "test suffix", mutate: func(m *Metadata) { m.DatabaseType = "GeoIP2-City-Test" }},
		{name: "extra suffix", mutate: func(m *Metadata) { m.DatabaseType = "GeoIP2-CityX" }},
		{name: "case change", mutate: func(m *Metadata) { m.DatabaseType = "geoip2-city" }},
		{name: "invalid database type utf8", mutate: func(m *Metadata) { m.DatabaseType = "GeoIP2-City\xff" }},
		{name: "major version", mutate: func(m *Metadata) { m.BinaryFormatMajorVersion = 3 }},
		{name: "minor version", mutate: func(m *Metadata) { m.BinaryFormatMinorVersion = 1 }},
		{name: "ip version", mutate: func(m *Metadata) { m.IPVersion = 5 }},
		{name: "zero nodes", mutate: func(m *Metadata) { m.NodeCount = 0 }},
		{name: "record size", mutate: func(m *Metadata) { m.RecordSize = 16 }},
		{name: "nil descriptions", mutate: func(m *Metadata) { m.Description = nil }},
		{name: "empty descriptions", mutate: func(m *Metadata) { m.Description = map[string]string{} }},
		{name: "invalid language utf8", mutate: func(m *Metadata) { m.Languages = []string{"\xff"} }},
		{
			name:   "invalid description language utf8",
			mutate: func(m *Metadata) { m.Description = map[string]string{"\xff": "test"} },
		},
		{name: "invalid description utf8", mutate: func(m *Metadata) { m.Description = map[string]string{"en": "\xff"} }},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := validMetadata("GeoIP2-City")
			test.mutate(&metadata)

			err := validateMetadata(metadata)
			if !errors.Is(err, ErrUnsupported) {
				t.Fatalf("validateMetadata() error = %v, want errors.Is(ErrUnsupported)", err)
			}
			if errors.Is(err, ErrCorrupt) {
				t.Fatalf("validateMetadata() error = %v, unexpectedly classified as corrupt", err)
			}
		})
	}
}

func TestOpenClassifiesCorruptInput(t *testing.T) {
	cityBytes, err := os.ReadFile(fixturePath("GeoIP2-City-Test.mmdb"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}

	tests := []struct {
		name string
		data []byte
	}{
		{name: "empty", data: []byte{}},
		{name: "arbitrary bytes", data: []byte("raw-source-content-must-not-appear")},
		{name: "truncated", data: cityBytes[:64]},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source")
			if err := os.WriteFile(path, test.data, 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}

			database, err := Open(path)
			if database != nil {
				t.Fatal("Open() returned a database for corrupt input")
			}
			if !errors.Is(err, ErrCorrupt) {
				t.Fatalf("Open() error = %v, want errors.Is(ErrCorrupt)", err)
			}
			if errors.Is(err, ErrUnsupported) || errors.Is(err, ErrOpen) {
				t.Fatalf("Open() error = %v, has incorrect classification", err)
			}
			if strings.Contains(err.Error(), string(test.data)) && len(test.data) > 0 {
				t.Fatalf("Open() error = %q, leaked input content", err)
			}
		})
	}
}

func TestOpenRejectsStructurallyCorruptCity(t *testing.T) {
	database, err := Open(fixturePath("GeoIP2-City-Test-Broken-Double-Format.mmdb"))
	if database != nil {
		t.Fatal("Open() returned a database for structurally corrupt input")
	}
	if !errors.Is(err, ErrCorrupt) {
		t.Fatalf("Open() error = %v, want errors.Is(ErrCorrupt)", err)
	}
	if errors.Is(err, ErrUnsupported) {
		t.Fatalf("Open() error = %v, unexpectedly classified as unsupported", err)
	}
}

func TestOpenClassifiesFilesystemFailuresWithoutPathLeakage(t *testing.T) {
	tests := []struct {
		name string
		path func(*testing.T) string
	}{
		{
			name: "missing file",
			path: func(t *testing.T) string {
				return filepath.Join(t.TempDir(), "user:super-secret@host")
			},
		},
		{
			name: "directory",
			path: func(t *testing.T) string {
				return t.TempDir()
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := test.path(t)
			database, err := Open(path)
			if database != nil {
				t.Fatal("Open() returned a database after filesystem failure")
			}
			if !errors.Is(err, ErrOpen) {
				t.Fatalf("Open() error = %v, want errors.Is(ErrOpen)", err)
			}
			if strings.Contains(err.Error(), path) || strings.Contains(err.Error(), "super-secret") {
				t.Fatalf("Open() error = %q, leaked caller path", err)
			}
			var pathError *os.PathError
			if errors.As(err, &pathError) {
				t.Fatalf("Open() error exposes unsafe path cause: %v", pathError)
			}
		})
	}
}

func TestOpenAllowsSymlinkWithoutMMDBSuffix(t *testing.T) {
	target, err := filepath.Abs(fixturePath("GeoIP2-City-Test.mmdb"))
	if err != nil {
		t.Fatalf("Abs() error = %v", err)
	}
	link := filepath.Join(t.TempDir(), "city-source")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("Symlink() unavailable: %v", err)
	}

	database, err := Open(link)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

func TestUpstreamFixtureStages(t *testing.T) {
	tests := []struct {
		name       string
		fixture    string
		typeName   string
		verifyFail bool
	}{
		{name: "valid city", fixture: "GeoIP2-City-Test.mmdb", typeName: "GeoIP2-City"},
		{name: "valid unsupported country", fixture: "GeoIP2-Country-Test.mmdb", typeName: "GeoIP2-Country"},
		{
			name:       "city failing full verification",
			fixture:    "GeoIP2-City-Test-Broken-Double-Format.mmdb",
			typeName:   "GeoIP2-City",
			verifyFail: true,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reader, err := maxminddb.Open(fixturePath(test.fixture))
			if err != nil {
				t.Fatalf("maxminddb.Open() error = %v", err)
			}
			if reader.Metadata.DatabaseType != test.typeName {
				t.Errorf("DatabaseType = %q, want %q", reader.Metadata.DatabaseType, test.typeName)
			}

			verifyErr := reader.Verify()
			if test.verifyFail && verifyErr == nil {
				t.Fatal("Verify() error = nil, want structural failure")
			}
			if !test.verifyFail && verifyErr != nil {
				t.Fatalf("Verify() error = %v", verifyErr)
			}
			if err := reader.Close(); err != nil {
				t.Fatalf("Close() error = %v", err)
			}
		})
	}
}

func TestFixtureChecksums(t *testing.T) {
	tests := []struct {
		name     string
		expected string
	}{
		{name: "GeoIP2-City-Test.mmdb", expected: "ed972738e4e03a3e56e12041a6af4d91592249d110f7e4a647e5f2fa0e639c09"},
		{name: "GeoIP2-Country-Test.mmdb", expected: "b37601903448683d241af52893c8cbf0fed461e0cdebe0bfaca01891fdeb6db9"},
		{
			name:     "GeoIP2-City-Test-Broken-Double-Format.mmdb",
			expected: "a340a6871b8bee8351befb8ad26f5229453705dddee0e948a35a65916c931e9c",
		},
		{name: "LICENSE-MIT", expected: "91276db973f25602d1aa43491f59cbc84cb88e6f151e1d0cc82a755563ce0195"},
		{name: "NOTICE", expected: "14770046606bd1f463f0346254a04d093e9e1794b82ed79cb0e754f5e17c0812"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			contents, err := os.ReadFile(fixturePath(test.name))
			if err != nil {
				t.Fatalf("ReadFile() error = %v", err)
			}
			digest := sha256.Sum256(contents)
			actual := hex.EncodeToString(digest[:])
			if actual != test.expected {
				t.Errorf("SHA-256 = %s, want %s", actual, test.expected)
			}
		})
	}
}

func fixturePath(name string) string {
	return filepath.Join("..", "..", "..", "testdata", "maxmind", name)
}

func assertMetadataEqual(t *testing.T, actual, expected Metadata) {
	t.Helper()

	if actual.DatabaseType != expected.DatabaseType ||
		actual.BinaryFormatMajorVersion != expected.BinaryFormatMajorVersion ||
		actual.BinaryFormatMinorVersion != expected.BinaryFormatMinorVersion ||
		actual.BuildEpoch != expected.BuildEpoch ||
		actual.IPVersion != expected.IPVersion ||
		actual.NodeCount != expected.NodeCount ||
		actual.RecordSize != expected.RecordSize ||
		!slices.Equal(actual.Languages, expected.Languages) ||
		!maps.Equal(actual.Description, expected.Description) {
		t.Errorf("Metadata() = %+v, want %+v", actual, expected)
	}
}
