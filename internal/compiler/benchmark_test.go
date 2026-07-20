package compiler

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/vikewoods/stategeodb/internal/source"
	maxmindsource "github.com/vikewoods/stategeodb/internal/source/maxmind"
)

const (
	benchmarkBuildEpoch int64 = 1_700_000_000
	benchmarkSourceID         = "benchmark"
)

var syntheticLocationCycle = [...]struct {
	country     string
	subdivision string
}{
	{country: "US", subdivision: "CA"},
	{country: "GB"},
	{},
}

func BenchmarkCompileFixture(b *testing.B) {
	sourcePath := filepath.Join("..", "..", "testdata", "maxmind", "GeoIP2-City-Test.mmdb")
	sourceSize := benchmarkFileSize(b, sourcePath)
	records, err := ingestBenchmarkSource(b.Context(), sourcePath, benchmarkSourceID)
	if err != nil {
		b.Fatalf("ingest fixture source: %v", err)
	}

	runCompileBenchmark(b, sourcePath, len(records), sourceSize)
}

func BenchmarkCompileSynthetic(b *testing.B) {
	for _, recordCount := range []int{1_000, 10_000, 100_000} {
		b.Run(fmt.Sprintf("records_%d", recordCount), func(b *testing.B) {
			directory := b.TempDir()
			sourcePath := filepath.Join(directory, "source.mmdb")
			sourceSize, err := writeSyntheticCitySource(
				sourcePath,
				recordCount,
				benchmarkBuildEpoch,
			)
			if err != nil {
				b.Fatalf("write synthetic source: %v", err)
			}

			runCompileBenchmark(b, sourcePath, recordCount, sourceSize)
		})
	}
}

func runCompileBenchmark(
	b *testing.B,
	sourcePath string,
	expectedRecords int,
	sourceSize int64,
) {
	b.Helper()
	workspaceRoot := b.TempDir()
	request := Request{
		SourcePath:    sourcePath,
		SourceID:      benchmarkSourceID,
		WorkspaceRoot: workspaceRoot,
		BuildEpoch:    benchmarkBuildEpoch,
	}

	b.ReportAllocs()
	b.ResetTimer()
	var observed benchmarkObservation
	iterations := 0
	for b.Loop() {
		candidate, err := Compile(b.Context(), request)

		b.StopTimer()
		if err != nil {
			b.Fatalf("Compile() error = %v", err)
		}
		actual, validationErr := observeBenchmarkCandidate(candidate, expectedRecords)
		var cleanupErr error
		if candidate != nil {
			cleanupErr = candidate.Cleanup()
		}
		if validationErr != nil {
			b.Fatal(validationErr)
		}
		if cleanupErr != nil {
			b.Fatalf("Cleanup() error = %v", cleanupErr)
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
	b.ReportMetric(float64(expectedRecords), "input-records/op")
	b.ReportMetric(float64(observed.outputNetworks), "output-networks/op")
	b.ReportMetric(float64(observed.comparedSegments), "compared-segments/op")
	b.ReportMetric(float64(sourceSize), "source-bytes/op")
	b.ReportMetric(float64(observed.candidateSize), "candidate-bytes/op")
	if elapsed := b.Elapsed(); elapsed > 0 {
		b.ReportMetric(float64(expectedRecords*b.N)/elapsed.Seconds(), "records/s")
	}
}

type benchmarkObservation struct {
	candidateSize    int64
	outputNetworks   int
	comparedSegments int
}

func observeBenchmarkCandidate(
	candidate *Candidate,
	expectedRecords int,
) (benchmarkObservation, error) {
	if candidate == nil {
		return benchmarkObservation{}, errors.New("Compile() candidate = nil")
	}
	if candidate.Size() <= 0 {
		return benchmarkObservation{}, fmt.Errorf("candidate Size() = %d, want positive", candidate.Size())
	}
	if candidate.InputRecordCount() != expectedRecords {
		return benchmarkObservation{}, fmt.Errorf(
			"candidate InputRecordCount() = %d, want %d",
			candidate.InputRecordCount(),
			expectedRecords,
		)
	}
	if candidate.BuildEpoch() != benchmarkBuildEpoch {
		return benchmarkObservation{}, fmt.Errorf(
			"candidate BuildEpoch() = %d, want %d",
			candidate.BuildEpoch(),
			benchmarkBuildEpoch,
		)
	}

	stats := candidate.EquivalenceStats()
	if stats.SourceRecords != expectedRecords {
		return benchmarkObservation{}, fmt.Errorf(
			"EquivalenceStats().SourceRecords = %d, want %d",
			stats.SourceRecords,
			expectedRecords,
		)
	}
	if stats.OutputNetworks <= 0 || stats.ComparedSegments <= 0 {
		return benchmarkObservation{}, fmt.Errorf(
			"EquivalenceStats() = %+v, want positive output and segment counts",
			stats,
		)
	}
	if stats.ComparedSegments < stats.SourceRecords || stats.ComparedSegments < stats.OutputNetworks {
		return benchmarkObservation{}, fmt.Errorf(
			"EquivalenceStats() = %+v, compared segments do not cover both streams",
			stats,
		)
	}

	return benchmarkObservation{
		candidateSize:    candidate.Size(),
		outputNetworks:   stats.OutputNetworks,
		comparedSegments: stats.ComparedSegments,
	}, nil
}

func TestWriteSyntheticCitySource(t *testing.T) {
	const recordCount = 7

	firstPath := filepath.Join(t.TempDir(), "first.mmdb")
	firstSize, err := writeSyntheticCitySource(firstPath, recordCount, benchmarkBuildEpoch)
	if err != nil {
		t.Fatalf("writeSyntheticCitySource(first) error = %v", err)
	}
	if firstSize <= 0 || firstSize != benchmarkFileSize(t, firstPath) {
		t.Fatalf("first source size = %d, want positive file size", firstSize)
	}

	assertSyntheticCityMetadata(t, firstPath)
	records, err := ingestBenchmarkSource(t.Context(), firstPath, "synthetic-test")
	if err != nil {
		t.Fatalf("ingestBenchmarkSource() error = %v", err)
	}
	if len(records) != recordCount {
		t.Fatalf("ingested record count = %d, want %d", len(records), recordCount)
	}

	var ipv4Count, ipv6Count int
	locations := make(map[[2]string]bool)
	for _, record := range records {
		if record.Prefix.Addr().Is4() {
			ipv4Count++
		} else {
			ipv6Count++
		}
		locations[[2]string{record.Country, record.Subdivision}] = true
	}
	if ipv4Count != 4 || ipv6Count != 3 {
		t.Errorf("address-family counts = IPv4 %d, IPv6 %d; want 4 and 3", ipv4Count, ipv6Count)
	}
	for _, location := range syntheticLocationCycle {
		key := [2]string{location.country, location.subdivision}
		if !locations[key] {
			t.Errorf("location shape %q/%q is missing", location.country, location.subdivision)
		}
	}

	secondPath := filepath.Join(t.TempDir(), "second.mmdb")
	secondSize, err := writeSyntheticCitySource(secondPath, recordCount, benchmarkBuildEpoch)
	if err != nil {
		t.Fatalf("writeSyntheticCitySource(second) error = %v", err)
	}
	firstContents, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatalf("ReadFile(first) error = %v", err)
	}
	secondContents, err := os.ReadFile(secondPath)
	if err != nil {
		t.Fatalf("ReadFile(second) error = %v", err)
	}
	if firstSize != secondSize || !bytes.Equal(firstContents, secondContents) {
		t.Error("same record count and build epoch produced different source bytes")
	}
}

func assertSyntheticCityMetadata(t *testing.T, path string) {
	t.Helper()
	database, err := maxminddb.Open(path)
	if err != nil {
		t.Fatalf("maxminddb.Open() error = %v", err)
	}
	if err := database.Verify(); err != nil {
		_ = database.Close()
		t.Fatalf("Verify() error = %v", err)
	}
	metadata := database.Metadata
	if err := database.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if metadata.DatabaseType != "GeoIP2-City" {
		t.Errorf("DatabaseType = %q, want GeoIP2-City", metadata.DatabaseType)
	}
	if metadata.BinaryFormatMajorVersion != 2 || metadata.BinaryFormatMinorVersion != 0 {
		t.Errorf(
			"binary format = %d.%d, want 2.0",
			metadata.BinaryFormatMajorVersion,
			metadata.BinaryFormatMinorVersion,
		)
	}
	if metadata.IPVersion != 6 {
		t.Errorf("IPVersion = %d, want 6", metadata.IPVersion)
	}
	if metadata.RecordSize != 28 {
		t.Errorf("RecordSize = %d, want 28", metadata.RecordSize)
	}
	if metadata.BuildEpoch != uint(benchmarkBuildEpoch) {
		t.Errorf("BuildEpoch = %d, want %d", metadata.BuildEpoch, benchmarkBuildEpoch)
	}
}

func writeSyntheticCitySource(path string, recordCount int, buildEpoch int64) (int64, error) {
	if recordCount <= 0 {
		return 0, fmt.Errorf("record count must be positive: %d", recordCount)
	}
	if buildEpoch <= 0 {
		return 0, fmt.Errorf("build epoch must be positive: %d", buildEpoch)
	}

	ipv4Count := (recordCount + 1) / 2
	if ipv4Count > 1<<16 {
		return 0, fmt.Errorf("IPv4 /24 record count exceeds 10.0.0.0/8 capacity: %d", ipv4Count)
	}

	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch:              buildEpoch,
		DatabaseType:            "GeoIP2-City",
		Description:             map[string]string{"en": "stategeodb deterministic synthetic City benchmark source"},
		DisableIPv4Aliasing:     false,
		IncludeReservedNetworks: true,
		IPVersion:               6,
		Languages:               []string{},
		RecordSize:              28,
		DisableMetadataPointers: false,
	})
	if err != nil {
		return 0, fmt.Errorf("create synthetic MMDB tree: %w", err)
	}

	for index := range ipv4Count {
		prefix := syntheticIPv4Prefix(index)
		if err := tree.Insert(syntheticPrefixNetwork(prefix), syntheticCityValue(index)); err != nil {
			return 0, fmt.Errorf("insert IPv4 record %d: %w", index, err)
		}
	}
	for index := range recordCount - ipv4Count {
		prefix := syntheticIPv6Prefix(index)
		if err := tree.Insert(
			syntheticPrefixNetwork(prefix),
			syntheticCityValue(ipv4Count+index),
		); err != nil {
			return 0, fmt.Errorf("insert IPv6 record %d: %w", index, err)
		}
	}

	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create synthetic source: %w", err)
	}
	written, err := tree.WriteTo(file)
	if err != nil {
		return 0, closeSyntheticSourceAfterError(file, fmt.Errorf("write synthetic source: %w", err))
	}
	if written <= 0 {
		return 0, closeSyntheticSourceAfterError(file, errors.New("write synthetic source: empty output"))
	}
	if err := file.Sync(); err != nil {
		return 0, closeSyntheticSourceAfterError(file, fmt.Errorf("synchronize synthetic source: %w", err))
	}
	info, err := file.Stat()
	if err != nil {
		return 0, closeSyntheticSourceAfterError(file, fmt.Errorf("stat synthetic source: %w", err))
	}
	if info.Size() != written {
		return 0, closeSyntheticSourceAfterError(
			file,
			fmt.Errorf("synthetic source size = %d, writer reported %d", info.Size(), written),
		)
	}
	if err := file.Close(); err != nil {
		return 0, fmt.Errorf("close synthetic source: %w", err)
	}
	return written, nil
}

func closeSyntheticSourceAfterError(file *os.File, primary error) error {
	if err := file.Close(); err != nil {
		return errors.Join(primary, fmt.Errorf("close synthetic source: %w", err))
	}
	return primary
}

func syntheticIPv4Prefix(index int) netip.Prefix {
	address := netip.AddrFrom4([4]byte{10, byte(index >> 8), byte(index), 0})
	return netip.PrefixFrom(address, 24)
}

func syntheticIPv6Prefix(index int) netip.Prefix {
	address := [16]byte{0x20, 0x01, 0x0d, 0xb8}
	binary.BigEndian.PutUint32(address[4:8], uint32(index))
	return netip.PrefixFrom(netip.AddrFrom16(address), 64)
}

func syntheticPrefixNetwork(prefix netip.Prefix) *net.IPNet {
	if prefix.Addr().Is4() {
		address := prefix.Addr().As4()
		return &net.IPNet{
			IP:   net.IP(address[:]),
			Mask: net.CIDRMask(prefix.Bits(), 32),
		}
	}

	address := prefix.Addr().As16()
	return &net.IPNet{
		IP:   net.IP(address[:]),
		Mask: net.CIDRMask(prefix.Bits(), 128),
	}
}

func syntheticCityValue(index int) mmdbtype.Map {
	location := syntheticLocationCycle[index%len(syntheticLocationCycle)]
	if location.country == "" {
		return mmdbtype.Map{}
	}

	value := mmdbtype.Map{
		"country": mmdbtype.Map{
			"iso_code": mmdbtype.String(location.country),
		},
	}
	if location.subdivision != "" {
		value["subdivisions"] = mmdbtype.Slice{
			mmdbtype.Map{
				"iso_code": mmdbtype.String(location.subdivision),
			},
		}
	}
	return value
}

func ingestBenchmarkSource(
	ctx context.Context,
	path string,
	sourceID string,
) ([]source.Record, error) {
	database, err := maxmindsource.Open(path)
	if err != nil {
		return nil, err
	}
	records, recordsErr := database.Records(ctx, sourceID)
	closeErr := database.Close()
	return records, errors.Join(recordsErr, closeErr)
}

func benchmarkFileSize(tb testing.TB, path string) int64 {
	tb.Helper()
	info, err := os.Stat(path)
	if err != nil {
		tb.Fatalf("Stat(%q) error = %v", path, err)
	}
	return info.Size()
}
