package compiler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/vikewoods/stategeodb/internal/mmdb"
	"github.com/vikewoods/stategeodb/internal/source"
)

func TestCompareRecordBehavior(t *testing.T) {
	tests := []struct {
		name            string
		sourceRecords   []source.Record
		outputRecords   []source.Record
		expectedStats   EquivalenceStats
		expectedError   error
		expectedMessage string
	}{
		{
			name: "identical interval streams",
			sourceRecords: []source.Record{
				testRecord(t, "192.0.2.0/24", "US", "CA", "source-a"),
				testRecord(t, "2001:db8::/32", "GB", "", "source-a"),
			},
			outputRecords: []source.Record{
				testRecord(t, "192.0.2.0/24", "US", "CA", "output-b"),
				testRecord(t, "2001:db8::/32", "GB", "", "output-b"),
			},
			expectedStats: EquivalenceStats{SourceRecords: 2, OutputNetworks: 2, ComparedSegments: 2},
		},
		{
			name: "source prefixes compacted in output",
			sourceRecords: []source.Record{
				testRecord(t, "192.0.2.0/25", "US", "CA", "source"),
				testRecord(t, "192.0.2.128/25", "US", "CA", "source"),
			},
			outputRecords: []source.Record{
				testRecord(t, "192.0.2.0/24", "US", "CA", "output"),
			},
			expectedStats: EquivalenceStats{SourceRecords: 2, OutputNetworks: 1, ComparedSegments: 2},
		},
		{
			name: "source prefix split in output",
			sourceRecords: []source.Record{
				testRecord(t, "2001:db8::/32", "GB", "ENG", "source"),
			},
			outputRecords: []source.Record{
				testRecord(t, "2001:db8::/33", "GB", "ENG", "output"),
				testRecord(t, "2001:db8:8000::/33", "GB", "ENG", "output"),
			},
			expectedStats: EquivalenceStats{SourceRecords: 1, OutputNetworks: 2, ComparedSegments: 2},
		},
		{
			name: "location mismatch inside source range",
			sourceRecords: []source.Record{
				testRecord(t, "192.0.2.0/24", "US", "CA", "source"),
			},
			outputRecords: []source.Record{
				testRecord(t, "192.0.2.0/25", "US", "CA", "output"),
				testRecord(t, "192.0.2.128/25", "US", "NY", "output"),
			},
			expectedError:   ErrNotEquivalent,
			expectedMessage: "location",
		},
		{
			name: "missing candidate coverage",
			sourceRecords: []source.Record{
				testRecord(t, "192.0.2.0/24", "US", "CA", "source"),
			},
			outputRecords: []source.Record{
				testRecord(t, "192.0.2.0/25", "US", "CA", "output"),
			},
			expectedError:   ErrNotEquivalent,
			expectedMessage: "actual absent",
		},
		{
			name: "unexpected candidate coverage",
			sourceRecords: []source.Record{
				testRecord(t, "192.0.2.0/25", "US", "CA", "source"),
			},
			outputRecords: []source.Record{
				testRecord(t, "192.0.2.0/24", "US", "CA", "output"),
			},
			expectedError:   ErrNotEquivalent,
			expectedMessage: "expected absent",
		},
		{
			name: "unknown location remains present",
			sourceRecords: []source.Record{
				testRecord(t, "198.51.100.0/24", "", "", "source"),
			},
			expectedError:   ErrNotEquivalent,
			expectedMessage: "actual absent",
		},
		{
			name: "address families compare independently",
			sourceRecords: []source.Record{
				testRecord(t, "0.0.0.0/0", "US", "", "source"),
				testRecord(t, "::/0", "GB", "", "source"),
			},
			outputRecords: []source.Record{
				testRecord(t, "::/0", "GB", "", "output"),
				testRecord(t, "0.0.0.0/0", "US", "", "output"),
			},
			expectedStats: EquivalenceStats{SourceRecords: 2, OutputNetworks: 2, ComparedSegments: 2},
		},
		{
			name: "single-address boundaries",
			sourceRecords: []source.Record{
				testRecord(t, "255.255.255.255/32", "US", "", "source"),
				testRecord(t, "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128", "GB", "", "source"),
			},
			outputRecords: []source.Record{
				testRecord(t, "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128", "GB", "", "output"),
				testRecord(t, "255.255.255.255/32", "US", "", "output"),
			},
			expectedStats: EquivalenceStats{SourceRecords: 2, OutputNetworks: 2, ComparedSegments: 2},
		},
		{
			name: "first-address boundaries",
			sourceRecords: []source.Record{
				testRecord(t, "0.0.0.0/32", "US", "", "source"),
				testRecord(t, "::/128", "GB", "", "source"),
			},
			outputRecords: []source.Record{
				testRecord(t, "::/128", "GB", "", "output"),
				testRecord(t, "0.0.0.0/32", "US", "", "output"),
			},
			expectedStats: EquivalenceStats{SourceRecords: 2, OutputNetworks: 2, ComparedSegments: 2},
		},
		{
			name: "overlapping source intervals",
			sourceRecords: []source.Record{
				testRecord(t, "192.0.2.0/24", "US", "", "source"),
				testRecord(t, "192.0.2.0/25", "US", "", "source"),
			},
			outputRecords: []source.Record{
				testRecord(t, "192.0.2.0/24", "US", "", "output"),
			},
			expectedError:   ErrNotEquivalent,
			expectedMessage: "overlapping source",
		},
		{
			name: "overlapping output intervals",
			sourceRecords: []source.Record{
				testRecord(t, "192.0.2.0/24", "US", "", "source"),
			},
			outputRecords: []source.Record{
				testRecord(t, "192.0.2.0/24", "US", "", "output"),
				testRecord(t, "192.0.2.128/25", "US", "", "output"),
			},
			expectedError:   ErrNotEquivalent,
			expectedMessage: "overlapping output",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			stats, err := compareRecordBehavior(t.Context(), test.sourceRecords, test.outputRecords)
			if !errors.Is(err, test.expectedError) {
				t.Fatalf("compareRecordBehavior() error = %v, want errors.Is(%v)", err, test.expectedError)
			}
			if test.expectedMessage != "" && !strings.Contains(err.Error(), test.expectedMessage) {
				t.Errorf("compareRecordBehavior() error = %q, want %q", err, test.expectedMessage)
			}
			if stats != test.expectedStats {
				t.Errorf("compareRecordBehavior() stats = %+v, want %+v", stats, test.expectedStats)
			}
		})
	}
}

func TestCompareRecordBehaviorShuffledStreams(t *testing.T) {
	sourceRecords := []source.Record{
		testRecord(t, "192.0.2.0/25", "US", "CA", "source-1"),
		testRecord(t, "192.0.2.128/25", "US", "CA", "source-2"),
		testRecord(t, "2001:db8::/33", "GB", "", "source-3"),
		testRecord(t, "2001:db8:8000::/33", "GB", "", "source-4"),
	}
	outputRecords := []source.Record{
		testRecord(t, "192.0.2.0/24", "US", "CA", "output-1"),
		testRecord(t, "2001:db8::/32", "GB", "", "output-2"),
	}
	expected := EquivalenceStats{SourceRecords: 4, OutputNetworks: 2, ComparedSegments: 4}

	for iteration := range 3 {
		slices.Reverse(sourceRecords)
		if iteration%2 == 0 {
			slices.Reverse(outputRecords)
		}
		stats, err := compareRecordBehavior(t.Context(), sourceRecords, outputRecords)
		if err != nil {
			t.Fatalf("iteration %d compareRecordBehavior() error = %v", iteration, err)
		}
		if stats != expected {
			t.Errorf("iteration %d stats = %+v, want %+v", iteration, stats, expected)
		}
	}
}

func TestFinalAddressBoundaries(t *testing.T) {
	tests := []struct {
		prefix   string
		expected string
	}{
		{prefix: "0.0.0.0/0", expected: "255.255.255.255"},
		{prefix: "255.255.255.255/32", expected: "255.255.255.255"},
		{prefix: "::/0", expected: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"},
		{prefix: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff/128", expected: "ffff:ffff:ffff:ffff:ffff:ffff:ffff:ffff"},
	}
	for _, test := range tests {
		t.Run(test.prefix, func(t *testing.T) {
			actual := finalAddress(netip.MustParsePrefix(test.prefix))
			if actual != netip.MustParseAddr(test.expected) {
				t.Errorf("finalAddress() = %s, want %s", actual, test.expected)
			}
		})
	}
}

func TestReadCandidateRecords(t *testing.T) {
	unknown := &fakeNetworkResult{prefix: netip.MustParsePrefix("198.51.100.0/24")}
	known := &fakeNetworkResult{
		prefix:      netip.MustParsePrefix("2001:db8::/32"),
		country:     stringPointerForCompiler("GB"),
		subdivision: stringPointerForCompiler("ENG"),
	}
	records, err := readCandidateRecords(
		t.Context(),
		resultsIterator(unknown, known),
		"request-source",
	)
	if err != nil {
		t.Fatalf("readCandidateRecords() error = %v", err)
	}
	expected := []source.Record{
		testRecord(t, "198.51.100.0/24", "", "", "request-source"),
		testRecord(t, "2001:db8::/32", "GB", "ENG", "request-source"),
	}
	if !slices.Equal(records, expected) {
		t.Errorf("readCandidateRecords() = %+v, want %+v", records, expected)
	}
	for _, result := range []*fakeNetworkResult{unknown, known} {
		result.assertMinimalPaths(t)
	}
}

func TestReadCandidateRecordsClassifiesFailures(t *testing.T) {
	unsafeCause := errors.New("unsafe offset and binary detail")
	tests := []struct {
		name          string
		result        *fakeNetworkResult
		expectedCause error
	}{
		{
			name:          "traversal",
			result:        &fakeNetworkResult{err: unsafeCause},
			expectedCause: ErrNotEquivalent,
		},
		{
			name: "country schema",
			result: &fakeNetworkResult{
				prefix:          netip.MustParsePrefix("192.0.2.0/24"),
				decodeError:     unsafeCause,
				decodeErrorPath: []any{"country", "iso_code"},
			},
			expectedCause: ErrNotEquivalent,
		},
		{
			name: "subdivision schema",
			result: &fakeNetworkResult{
				prefix:          netip.MustParsePrefix("192.0.2.0/24"),
				country:         stringPointerForCompiler("US"),
				decodeError:     unsafeCause,
				decodeErrorPath: []any{"subdivisions", 0, "iso_code"},
			},
			expectedCause: ErrNotEquivalent,
		},
		{
			name: "invalid prefix",
			result: &fakeNetworkResult{
				prefix:  netip.MustParsePrefix("192.0.2.1/24"),
				country: stringPointerForCompiler("US"),
			},
			expectedCause: source.ErrInvalidPrefix,
		},
		{
			name: "invalid prefix precedes schema failure",
			result: &fakeNetworkResult{
				prefix:          netip.MustParsePrefix("192.0.2.1/24"),
				decodeError:     unsafeCause,
				decodeErrorPath: []any{"country", "iso_code"},
			},
			expectedCause: source.ErrInvalidPrefix,
		},
		{
			name: "invalid country",
			result: &fakeNetworkResult{
				prefix:  netip.MustParsePrefix("192.0.2.0/24"),
				country: stringPointerForCompiler("USA"),
			},
			expectedCause: source.ErrInvalidCountry,
		},
		{
			name: "noncanonical country",
			result: &fakeNetworkResult{
				prefix:  netip.MustParsePrefix("192.0.2.0/24"),
				country: stringPointerForCompiler("us"),
			},
			expectedCause: source.ErrInvalidCountry,
		},
		{
			name: "invalid subdivision",
			result: &fakeNetworkResult{
				prefix:      netip.MustParsePrefix("192.0.2.0/24"),
				country:     stringPointerForCompiler("US"),
				subdivision: stringPointerForCompiler("CALI"),
			},
			expectedCause: source.ErrInvalidSubdivision,
		},
		{
			name: "noncanonical subdivision",
			result: &fakeNetworkResult{
				prefix:      netip.MustParsePrefix("192.0.2.0/24"),
				country:     stringPointerForCompiler("US"),
				subdivision: stringPointerForCompiler("ca"),
			},
			expectedCause: source.ErrInvalidSubdivision,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			records, err := readCandidateRecords(
				t.Context(),
				resultsIterator(test.result),
				"primary",
			)
			if records != nil {
				t.Errorf("readCandidateRecords() records = %+v, want nil", records)
			}
			for _, target := range []error{ErrNotEquivalent, test.expectedCause} {
				if !errors.Is(err, target) {
					t.Errorf("readCandidateRecords() error = %v, want errors.Is(%v)", err, target)
				}
			}
			if strings.Contains(err.Error(), unsafeCause.Error()) {
				t.Errorf("readCandidateRecords() error exposed unsafe detail: %v", err)
			}
			if test.name == "invalid prefix precedes schema failure" && len(test.result.paths) != 0 {
				t.Errorf("DecodePath() calls = %d, want 0 after invalid prefix", len(test.result.paths))
			}
		})
	}
}

func TestCandidateTraversalUsesCanonicalNetworks(t *testing.T) {
	records := []source.Record{
		testRecord(t, "192.0.2.0/24", "US", "CA", "source"),
		testRecord(t, "198.51.100.0/24", "", "", "source"),
		testRecord(t, "2001:db8::/32", "GB", "", "source"),
	}
	var destination bytes.Buffer
	if _, err := mmdb.Write(&destination, records, mmdb.Options{BuildEpoch: testBuildEpoch}); err != nil {
		t.Fatalf("mmdb.Write() error = %v", err)
	}

	database, err := maxminddb.OpenBytes(destination.Bytes())
	if err != nil {
		t.Fatalf("OpenBytes() error = %v", err)
	}
	defer database.Close()
	defaultCount := countUpstreamNetworks(t, database.Networks())
	aliasedCount := countUpstreamNetworks(
		t,
		database.Networks(maxminddb.IncludeAliasedNetworks()),
	)
	if aliasedCount <= defaultCount {
		t.Fatalf("aliased network count = %d, want greater than default %d", aliasedCount, defaultCount)
	}

	verification, err := defaultOperations().openVerification(destination.Bytes())
	if err != nil {
		t.Fatalf("openVerification() error = %v", err)
	}
	output, err := readCandidateRecords(t.Context(), verification.Networks(), "different-source")
	if err != nil {
		t.Fatalf("readCandidateRecords() error = %v", err)
	}
	if len(output) != defaultCount {
		t.Errorf("output network count = %d, want default iterator count %d", len(output), defaultCount)
	}
	if _, err := compareRecordBehavior(t.Context(), records, output); err != nil {
		t.Errorf("compareRecordBehavior() error = %v", err)
	}
	if err := verification.Close(); err != nil {
		t.Fatalf("verification Close() error = %v", err)
	}
}

func TestVerifyCandidateClosesReaderAfterEquivalenceFailure(t *testing.T) {
	closeCalls := 0
	unsafeClose := errors.New("unsafe verifier close detail")
	database := &fakeVerificationDatabase{
		metadata: expectedMetadata(testBuildEpoch),
		networks: []networkResult{
			&fakeNetworkResult{
				prefix:  netip.MustParsePrefix("192.0.2.0/24"),
				country: stringPointerForCompiler("GB"),
			},
		},
		closeErr:   unsafeClose,
		closeCalls: &closeCalls,
	}
	open := func([]byte) (verificationDatabase, error) {
		return database, nil
	}
	sourceRecords := []source.Record{
		testRecord(t, "192.0.2.0/24", "US", "", "source"),
	}
	stats, err := verifyCandidate(
		t.Context(),
		[]byte("unused by fake"),
		testBuildEpoch,
		"request-source",
		sourceRecords,
		open,
		compareRecordBehavior,
	)
	if stats != (EquivalenceStats{}) {
		t.Errorf("verifyCandidate() stats = %+v, want zero", stats)
	}
	for _, target := range []error{ErrNotEquivalent, ErrVerify} {
		if !errors.Is(err, target) {
			t.Errorf("verifyCandidate() error = %v, want errors.Is(%v)", err, target)
		}
	}
	if closeCalls != 1 {
		t.Errorf("verification close calls = %d, want 1", closeCalls)
	}
	if strings.Contains(err.Error(), unsafeClose.Error()) {
		t.Errorf("verifyCandidate() error exposed unsafe detail: %v", err)
	}
}

func TestVerifyCandidateClosesReaderAfterTraversalCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	closeCalls := 0
	database := &fakeVerificationDatabase{
		metadata: expectedMetadata(testBuildEpoch),
		networks: []networkResult{
			&fakeNetworkResult{prefix: netip.MustParsePrefix("192.0.2.0/24")},
		},
		networkHook: func(int) { cancel() },
		closeCalls:  &closeCalls,
	}
	open := func([]byte) (verificationDatabase, error) {
		return database, nil
	}
	stats, err := verifyCandidate(
		ctx,
		[]byte("unused by fake"),
		testBuildEpoch,
		"request-source",
		[]source.Record{testRecord(t, "192.0.2.0/24", "", "", "source")},
		open,
		compareRecordBehavior,
	)
	if stats != (EquivalenceStats{}) {
		t.Errorf("verifyCandidate() stats = %+v, want zero", stats)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("verifyCandidate() error = %v, want errors.Is(context.Canceled)", err)
	}
	if errors.Is(err, ErrNotEquivalent) {
		t.Errorf("verifyCandidate() cancellation classified as ErrNotEquivalent: %v", err)
	}
	if closeCalls != 1 {
		t.Errorf("verification close calls = %d, want 1", closeCalls)
	}
}

func TestCompareRecordBehaviorCancellation(t *testing.T) {
	records := []source.Record{
		testRecord(t, "192.0.2.0/25", "US", "", "source"),
		testRecord(t, "192.0.2.128/25", "US", "", "source"),
	}
	ctx := &cancelAfterErrChecksContext{
		Context:   t.Context(),
		remaining: 11,
		err:       context.Canceled,
	}
	stats, err := compareRecordBehavior(ctx, records, records)
	if stats != (EquivalenceStats{}) {
		t.Errorf("compareRecordBehavior() stats = %+v, want zero", stats)
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("compareRecordBehavior() error = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrNotEquivalent) {
		t.Errorf("cancellation error = %v, unexpectedly classified as non-equivalence", err)
	}
}

type cancelAfterErrChecksContext struct {
	context.Context
	remaining int
	err       error
}

func (ctx *cancelAfterErrChecksContext) Err() error {
	if ctx.remaining == 0 {
		return ctx.err
	}
	ctx.remaining--
	return nil
}

func testRecord(
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
		t.Fatalf("source.NewRecord() error = %v", err)
	}
	return record
}

type fakeNetworkResult struct {
	prefix          netip.Prefix
	country         *string
	subdivision     *string
	err             error
	decodeError     error
	decodeErrorPath []any
	paths           [][]any
}

func (result *fakeNetworkResult) Err() error {
	return result.err
}

func (result *fakeNetworkResult) Prefix() netip.Prefix {
	return result.prefix
}

func (result *fakeNetworkResult) DecodePath(destination any, path ...any) error {
	result.paths = append(result.paths, slices.Clone(path))
	if result.decodeError != nil && pathsEqual(path, result.decodeErrorPath...) {
		return result.decodeError
	}
	target, ok := destination.(**string)
	if !ok {
		return fmt.Errorf("unexpected destination type")
	}
	switch {
	case pathsEqual(path, "country", "iso_code"):
		*target = result.country
	case pathsEqual(path, "subdivisions", 0, "iso_code"):
		*target = result.subdivision
	default:
		return fmt.Errorf("unexpected decode path")
	}
	return nil
}

func (result *fakeNetworkResult) assertMinimalPaths(t *testing.T) {
	t.Helper()
	expected := [][]any{
		{"country", "iso_code"},
		{"subdivisions", 0, "iso_code"},
	}
	if len(result.paths) != len(expected) {
		t.Fatalf("DecodePath() calls = %d, want %d", len(result.paths), len(expected))
	}
	for index := range expected {
		if !pathsEqual(result.paths[index], expected[index]...) {
			t.Errorf("DecodePath() call %d = %v, want %v", index, result.paths[index], expected[index])
		}
	}
}

func resultsIterator(results ...networkResult) networkIterator {
	return func(yield func(networkResult) bool) {
		for _, result := range results {
			if !yield(result) {
				return
			}
		}
	}
}

func pathsEqual(actual []any, expected ...any) bool {
	return slices.EqualFunc(actual, expected, func(left any, right any) bool {
		return left == right
	})
}

func stringPointerForCompiler(value string) *string {
	return &value
}

func countUpstreamNetworks(
	t *testing.T,
	networks func(func(maxminddb.Result) bool),
) int {
	t.Helper()
	var count int
	for result := range networks {
		if err := result.Err(); err != nil {
			t.Fatalf("Networks() error = %v", err)
		}
		count++
	}
	return count
}
