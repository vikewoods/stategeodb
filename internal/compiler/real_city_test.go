package compiler

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	maxmindsource "github.com/vikewoods/stategeodb/internal/source/maxmind"
)

const (
	realCityEnvironment = "STATEGEODB_REAL_CITY_DB"
	realCitySourceID    = "geolite2-city-real"
	realCityEmptyError  = "STATEGEODB_REAL_CITY_DB is set but empty"
)

type realCitySource struct {
	path         string
	baseName     string
	checksum     string
	databaseType string
	fileInfo     os.FileInfo
	size         int64
	buildEpoch   int64
	recordCount  int
}

type realCityCandidateObservation struct {
	inputRecords     int
	outputNetworks   int
	comparedSegments int
	candidateSize    int64
}

func TestCompileRealCity(t *testing.T) {
	path := requireRealCityPath(t)
	source, err := validateRealCitySource(t.Context(), path)
	if err != nil {
		t.Fatal(err)
	}

	candidate, err := Compile(t.Context(), Request{
		SourcePath:    source.path,
		SourceID:      realCitySourceID,
		WorkspaceRoot: t.TempDir(),
		BuildEpoch:    source.buildEpoch,
	})
	if err != nil {
		t.Fatalf("Compile() failed: %v", err)
	}
	if candidate != nil {
		t.Cleanup(func() {
			if err := cleanupRealCityCandidate(candidate); err != nil {
				t.Errorf("candidate fallback cleanup failed: %v", err)
			}
		})
	}
	if err := confirmRealCitySourceUnchanged(source); err != nil {
		t.Fatal(err)
	}

	observation, err := observeRealCityCandidate(candidate, source)
	if err != nil {
		t.Fatal(err)
	}
	verifyRealCityCandidate(t, candidate, source.buildEpoch)
	if err := cleanupRealCityCandidate(candidate); err != nil {
		t.Fatalf("candidate cleanup failed: %v", err)
	}

	t.Logf(
		"real City source: basename=%q sha256=%s source_bytes=%d build_epoch=%d "+
			"database_type=%q input_records=%d output_networks=%d "+
			"compared_segments=%d candidate_bytes=%d",
		source.baseName,
		source.checksum,
		source.size,
		source.buildEpoch,
		source.databaseType,
		observation.inputRecords,
		observation.outputNetworks,
		observation.comparedSegments,
		observation.candidateSize,
	)
}

func BenchmarkCompileRealCity(b *testing.B) {
	path := requireRealCityPath(b)
	source, err := validateRealCitySource(b.Context(), path)
	if err != nil {
		b.Fatal(err)
	}
	workspaceRoot := b.TempDir()
	request := Request{
		SourcePath:    source.path,
		SourceID:      realCitySourceID,
		WorkspaceRoot: workspaceRoot,
		BuildEpoch:    source.buildEpoch,
	}

	// Source validation materializes every record. Collect that setup garbage
	// before resetting Go 1.26's timer and allocation-counter snapshots.
	runtime.GC()
	b.ReportAllocs()
	b.ResetTimer()
	var observed realCityCandidateObservation
	iterations := 0
	for b.Loop() {
		candidate, err := Compile(b.Context(), request)

		// Go 1.26 StopTimer snapshots TotalAlloc and Mallocs, so validation and
		// cleanup allocations between StopTimer and StartTimer are excluded.
		b.StopTimer()
		if err != nil {
			b.Fatalf("Compile() failed: %v", err)
		}
		sourceValidationErr := confirmRealCitySourceUnchanged(source)
		actual, validationErr := observeRealCityCandidate(candidate, source)
		cleanupErr := cleanupRealCityCandidate(candidate)
		if sourceValidationErr != nil {
			b.Fatal(sourceValidationErr)
		}
		if validationErr != nil {
			b.Fatal(validationErr)
		}
		if cleanupErr != nil {
			b.Fatalf("candidate cleanup failed: %v", cleanupErr)
		}
		if iterations > 0 && actual != observed {
			b.Fatalf("compile metrics changed between iterations: got %+v, want %+v", actual, observed)
		}
		observed = actual
		iterations++
		b.StartTimer()
	}

	if iterations == 0 {
		b.Fatal("benchmark completed without a compile iteration")
	}
	b.ReportMetric(float64(observed.inputRecords), "input_records/op")
	b.ReportMetric(float64(observed.outputNetworks), "output_networks/op")
	b.ReportMetric(float64(observed.comparedSegments), "compared_segments/op")
	b.ReportMetric(float64(source.size), "source_bytes/op")
	b.ReportMetric(float64(observed.candidateSize), "candidate_bytes/op")
	if elapsed := b.Elapsed(); elapsed > 0 {
		recordsProcessed := float64(observed.inputRecords) * float64(b.N)
		b.ReportMetric(recordsProcessed/elapsed.Seconds(), "records/s")
	}
}

func TestParseRealCityEnvironment(t *testing.T) {
	tests := []struct {
		name            string
		value           string
		present         bool
		expectedPath    string
		expectedEnabled bool
		expectedError   string
	}{
		{name: "absent"},
		{
			name:            "empty",
			present:         true,
			expectedEnabled: true,
			expectedError:   realCityEmptyError,
		},
		{
			name:            "configured",
			value:           "/private/example/GeoLite2-City.mmdb",
			present:         true,
			expectedPath:    "/private/example/GeoLite2-City.mmdb",
			expectedEnabled: true,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path, enabled, err := parseRealCityEnvironment(test.value, test.present)
			if path != test.expectedPath || enabled != test.expectedEnabled {
				t.Errorf("parseRealCityEnvironment() = %q, %t; want %q, %t", path, enabled, test.expectedPath, test.expectedEnabled)
			}
			actualError := ""
			if err != nil {
				actualError = err.Error()
			}
			if actualError != test.expectedError {
				t.Errorf("parseRealCityEnvironment() error = %q, want %q", actualError, test.expectedError)
			}
		})
	}
}

func requireRealCityPath(tb testing.TB) string {
	tb.Helper()
	value, present := os.LookupEnv(realCityEnvironment)
	path, enabled, err := parseRealCityEnvironment(value, present)
	if !enabled {
		tb.Skip(realCityEnvironment + " is not set")
	}
	if err != nil {
		tb.Fatal(err)
	}
	return path
}

func parseRealCityEnvironment(value string, present bool) (string, bool, error) {
	if !present {
		return "", false, nil
	}
	if value == "" {
		return "", true, errors.New(realCityEmptyError)
	}
	return value, true, nil
}

func validateRealCitySource(ctx context.Context, path string) (realCitySource, error) {
	pathInfo, err := os.Lstat(path)
	if err != nil {
		return realCitySource{}, errors.New("private City database is not readable")
	}
	if !pathInfo.Mode().IsRegular() || pathInfo.Size() <= 0 {
		return realCitySource{}, errors.New("private City database is not a regular file")
	}
	checksum, err := hashRealCityFile(path, pathInfo)
	if err != nil {
		return realCitySource{}, err
	}

	database, err := maxmindsource.Open(path)
	if err != nil {
		return realCitySource{}, err
	}
	metadata := database.Metadata()
	if metadata.DatabaseType != "GeoLite2-City" {
		_ = database.Close()
		return realCitySource{}, errors.New("private City database type is not GeoLite2-City")
	}
	if metadata.BinaryFormatMajorVersion != 2 || metadata.BinaryFormatMinorVersion != 0 {
		_ = database.Close()
		return realCitySource{}, errors.New("private City database binary format is unsupported")
	}
	if metadata.BuildEpoch == 0 || uint64(metadata.BuildEpoch) > math.MaxInt64 {
		_ = database.Close()
		return realCitySource{}, errors.New("private City database build epoch is invalid")
	}
	records, recordsErr := database.Records(ctx, realCitySourceID)
	closeErr := database.Close()
	if recordsErr != nil || closeErr != nil {
		return realCitySource{}, errors.Join(recordsErr, closeErr)
	}
	if len(records) == 0 {
		return realCitySource{}, errors.New("private City database contains no normalized records")
	}
	hasIPv4 := false
	hasIPv6 := false
	for _, record := range records {
		if record.Prefix.Addr().Is4() {
			hasIPv4 = true
		} else {
			hasIPv6 = true
		}
	}
	if !hasIPv4 || !hasIPv6 {
		return realCitySource{}, errors.New("private City database does not contain both address families")
	}

	source := realCitySource{
		path:         path,
		baseName:     filepath.Base(path),
		checksum:     checksum,
		databaseType: metadata.DatabaseType,
		fileInfo:     pathInfo,
		size:         pathInfo.Size(),
		buildEpoch:   int64(metadata.BuildEpoch),
		recordCount:  len(records),
	}
	if err := confirmRealCitySourceUnchanged(source); err != nil {
		return realCitySource{}, err
	}
	return source, nil
}

func hashRealCityFile(path string, expected os.FileInfo) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", errors.New("private City database is not readable")
	}
	fileInfo, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return "", errors.New("private City database cannot be inspected")
	}
	if !fileInfo.Mode().IsRegular() || !os.SameFile(expected, fileInfo) || fileInfo.Size() != expected.Size() {
		_ = file.Close()
		return "", errors.New("private City database changed before hashing")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		_ = file.Close()
		return "", errors.New("private City database cannot be hashed")
	}
	if err := file.Close(); err != nil {
		return "", errors.New("private City database cannot be closed after hashing")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func confirmRealCitySourceUnchanged(source realCitySource) error {
	currentInfo, err := os.Lstat(source.path)
	if err != nil || !os.SameFile(source.fileInfo, currentInfo) || currentInfo.Size() != source.size {
		return errors.New("private City database changed during validation or compilation")
	}
	checksum, err := hashRealCityFile(source.path, source.fileInfo)
	if err != nil {
		return err
	}
	if checksum != source.checksum {
		return errors.New("private City database contents changed during validation or compilation")
	}
	return nil
}

func observeRealCityCandidate(
	candidate *Candidate,
	source realCitySource,
) (realCityCandidateObservation, error) {
	if candidate == nil {
		return realCityCandidateObservation{}, errors.New("Compile() candidate is nil")
	}
	if candidate.InputRecordCount() <= 0 || candidate.InputRecordCount() != source.recordCount {
		return realCityCandidateObservation{}, errors.New("candidate input-record count is invalid")
	}
	if candidate.BuildEpoch() != source.buildEpoch {
		return realCityCandidateObservation{}, errors.New("candidate build epoch does not match the source")
	}
	if candidate.Size() <= 0 {
		return realCityCandidateObservation{}, errors.New("candidate size is not positive")
	}

	stats := candidate.EquivalenceStats()
	if stats.SourceRecords != candidate.InputRecordCount() {
		return realCityCandidateObservation{}, errors.New("candidate source-record statistics are inconsistent")
	}
	if stats.OutputNetworks <= 0 || stats.ComparedSegments <= 0 {
		return realCityCandidateObservation{}, errors.New("candidate equivalence statistics are not positive")
	}
	if stats.ComparedSegments < stats.SourceRecords || stats.ComparedSegments < stats.OutputNetworks {
		return realCityCandidateObservation{}, errors.New("candidate compared segments do not cover both streams")
	}

	return realCityCandidateObservation{
		inputRecords:     candidate.InputRecordCount(),
		outputNetworks:   stats.OutputNetworks,
		comparedSegments: stats.ComparedSegments,
		candidateSize:    candidate.Size(),
	}, nil
}

func cleanupRealCityCandidate(candidate *Candidate) error {
	if candidate == nil {
		return errors.New("candidate cleanup received nil candidate")
	}
	candidatePath := candidate.Path()
	if candidatePath == "" {
		return errors.New("candidate path identity validation failed before cleanup")
	}
	workspacePath := filepath.Dir(candidatePath)
	if err := candidate.Cleanup(); err != nil {
		return err
	}
	if _, err := os.Lstat(workspacePath); !errors.Is(err, os.ErrNotExist) {
		return errors.New("candidate workspace remains after cleanup")
	}
	return nil
}

func verifyRealCityCandidate(t *testing.T, candidate *Candidate, buildEpoch int64) {
	t.Helper()
	candidatePath := candidate.Path()
	if candidatePath == "" {
		t.Fatal("candidate path identity validation failed before independent verification")
	}
	database, err := maxminddb.Open(candidatePath)
	if err != nil {
		t.Fatal("candidate failed independent open")
	}
	defer func() {
		if err := database.Close(); err != nil {
			t.Error("candidate failed independent close")
		}
	}()
	if err := database.Verify(); err != nil {
		t.Fatal("candidate failed independent structural verification")
	}
	assertCandidateMetadata(t, database.Metadata, buildEpoch)
	assertRealCityRuntimeSchema(t, database)
}

func assertRealCityRuntimeSchema(t *testing.T, database *maxminddb.Reader) {
	t.Helper()
	for result := range database.Networks() {
		if result.Err() != nil {
			t.Fatal("candidate traversal failed during runtime-schema validation")
		}
		var runtimeValue runtimeRecord
		if err := result.Decode(&runtimeValue); err != nil {
			t.Fatal("candidate record failed runtime-schema decoding")
		}
		var raw map[string]any
		if err := result.Decode(&raw); err != nil {
			t.Fatal("candidate record failed raw-schema decoding")
		}
		if len(raw) == 0 {
			continue
		}
		assertMinimalRuntimeMap(t, raw)
		return
	}
	t.Fatal("candidate contains no non-empty runtime record")
}

func assertMinimalRuntimeMap(t *testing.T, raw map[string]any) {
	t.Helper()
	for key := range raw {
		if key != "country" && key != "subdivisions" {
			t.Fatalf("candidate runtime schema contains unexpected key %q", key)
		}
	}
	country, ok := raw["country"].(map[string]any)
	if !ok || len(country) != 1 {
		t.Fatal("candidate runtime country schema is invalid")
	}
	if _, ok := country["iso_code"].(string); !ok {
		t.Fatal("candidate runtime country code schema is invalid")
	}

	subdivisionsValue, hasSubdivisions := raw["subdivisions"]
	if !hasSubdivisions {
		return
	}
	subdivisions, ok := subdivisionsValue.([]any)
	if !ok || len(subdivisions) != 1 {
		t.Fatal("candidate runtime subdivisions schema is invalid")
	}
	first, ok := subdivisions[0].(map[string]any)
	if !ok || len(first) != 1 {
		t.Fatal("candidate runtime first-subdivision schema is invalid")
	}
	if _, ok := first["iso_code"].(string); !ok {
		t.Fatal("candidate runtime subdivision code schema is invalid")
	}
}
