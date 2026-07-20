package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/netip"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/vikewoods/stategeodb/internal/artifactprofile"
	"github.com/vikewoods/stategeodb/internal/compiler"
	"github.com/vikewoods/stategeodb/internal/inspect"
	"github.com/vikewoods/stategeodb/internal/mmdb"
	"github.com/vikewoods/stategeodb/internal/source"
)

func TestParseInspectArguments(t *testing.T) {
	addresses := make([]string, maxInspectLookups)
	args := []string{"--database", "/private/generated.mmdb"}
	for index := range maxInspectLookups {
		addresses[index] = "192.0.2." + strconv.Itoa(index+1)
		args = append(args, "--ip", addresses[index])
	}
	request, ok := parseInspectArguments(args)
	if !ok || request.DatabasePath != "/private/generated.mmdb" || len(request.Addresses) != maxInspectLookups {
		t.Fatalf("parseInspectArguments() = %#v/%t", request, ok)
	}
	for index, address := range request.Addresses {
		if address.String() != addresses[index] {
			t.Errorf("Addresses[%d] = %s, want %s", index, address, addresses[index])
		}
	}

	tests := [][]string{
		{},
		{"--database", ""},
		{"--database", "one", "--database", "two"},
		{"--database", "database.mmdb", "--ip", ""},
		{"--database", "database.mmdb", "--ip", "not-an-ip"},
		{"--database", "database.mmdb", "--ip", "fe80::1%eth0"},
		{"--database", "database.mmdb", "positional"},
		{"--database", "database.mmdb", "--unknown"},
		{"--database", "database.mmdb", "--record-size", "28"},
		{"--database", "database.mmdb", "--"},
	}
	tooMany := []string{"--database", "database.mmdb"}
	for index := range maxInspectLookups + 1 {
		tooMany = append(tooMany, "--ip", "192.0.2."+strconv.Itoa(index+1))
	}
	tests = append(tests, tooMany)
	for _, args := range tests {
		if request, ok := parseInspectArguments(args); ok {
			t.Errorf("parseInspectArguments(%q) = %#v/true, want false", args, request)
		}
	}
}

func TestRunInspect_ParsingFailsBeforeInspection(t *testing.T) {
	invalid := [][]string{
		{"inspect"},
		{"inspect", "--database", "database.mmdb", "--ip", "secret-ip"},
		{"inspect", "--database", "one", "--database", "two"},
		{"inspect", "--database", "database.mmdb", "extra"},
	}
	for _, args := range invalid {
		called := false
		status, stdout, stderr := runCLIWithInspectOperations(t, t.Context(), args, &bytes.Buffer{}, &bytes.Buffer{}, inspectOperations{
			execute: func(context.Context, inspect.Request) (inspect.Result, error) {
				called = true
				return inspect.Result{}, errors.New("unexpected inspection")
			},
		})
		if status != exitFailure || stdout != "" || stderr != invalidInspectUsageText || called {
			t.Errorf("run(%q) = %d/%q/%q/called=%t", args, status, stdout, stderr, called)
		}
		for _, argument := range args {
			if strings.Contains(stderr, argument) && argument != "inspect" {
				t.Errorf("stderr exposed argument %q: %q", argument, stderr)
			}
		}
	}
}

func TestRunInspect_ExactOutput(t *testing.T) {
	result := validInspectResultFixture()
	var captured inspect.Request
	status, stdout, stderr := runCLIWithInspectOperations(
		t,
		t.Context(),
		[]string{"inspect", "--database", "/private/generated.mmdb", "--ip", "::ffff:192.0.2.1", "--ip", "203.0.113.1"},
		&bytes.Buffer{},
		&bytes.Buffer{},
		inspectOperations{execute: func(_ context.Context, request inspect.Request) (inspect.Result, error) {
			captured = request
			return result, nil
		}},
	)
	expected := "database_type=" + mmdb.DatabaseType + "\n" +
		"schema_version=1\n" +
		"build_epoch=1700000123\n" +
		"binary_format=2.0\n" +
		"ip_version=6\n" +
		"record_size=" + strconv.Itoa(mmdb.RecordSize) + "\n" +
		"node_count=9\n" +
		"lookup_count=2\n" +
		"lookup_1_ip=192.0.2.1\n" +
		"lookup_1_found=true\n" +
		"lookup_1_network=192.0.2.0/24\n" +
		"lookup_1_country=US\n" +
		"lookup_1_subdivision=CA\n" +
		"lookup_2_ip=203.0.113.1\n" +
		"lookup_2_found=false\n" +
		"lookup_2_network=\n" +
		"lookup_2_country=\n" +
		"lookup_2_subdivision=\n"
	if status != exitSuccess || stdout != expected || stderr != "" {
		t.Errorf("run() = %d/%q/%q, want success/exact output/empty", status, stdout, stderr)
	}
	if captured.DatabasePath != "/private/generated.mmdb" ||
		captured.Addresses[0] != netip.MustParseAddr("::ffff:192.0.2.1") {
		t.Errorf("captured request = %#v", captured)
	}
	if strings.Contains(stdout, "/private/generated.mmdb") {
		t.Error("stdout exposed database path")
	}
}

func TestRunInspect_MetadataOnly(t *testing.T) {
	result := validInspectResultFixture()
	result.Lookups = []inspect.Lookup{}
	status, stdout, stderr := runCLIWithInspectOperations(
		t,
		t.Context(),
		[]string{"inspect", "--database", "generated.mmdb"},
		&bytes.Buffer{},
		&bytes.Buffer{},
		inspectOperations{execute: func(_ context.Context, request inspect.Request) (inspect.Result, error) {
			if len(request.Addresses) != 0 {
				t.Errorf("Addresses = %v, want empty", request.Addresses)
			}
			return result, nil
		}},
	)
	if status != exitSuccess || stderr != "" || !strings.HasSuffix(stdout, "lookup_count=0\n") || strings.Contains(stdout, "lookup_1_") {
		t.Errorf("metadata-only = %d/%q/%q", status, stdout, stderr)
	}
}

func TestFormatInspectOutput_RejectsUnsupportedRecordSizes(t *testing.T) {
	result := validInspectResultFixture()
	for _, recordSize := range []uint{28, 32} {
		result.Metadata.RecordSize = recordSize
		if _, ok := formatInspectOutput(result); ok {
			t.Errorf("formatInspectOutput() accepted record size %d", recordSize)
		}
	}
}

func TestFormatInspectOutput_RejectsNonUSSubdivision(t *testing.T) {
	result := validInspectResultFixture()
	result.Lookups[0].Country = "GB"
	result.Lookups[0].Subdivision = "ENG"
	if _, ok := formatInspectOutput(result); ok {
		t.Error("formatInspectOutput() accepted a non-US subdivision")
	}
}

func TestRunInspect_FailuresWriteNoStdout(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		diagnostic string
	}{
		{name: "invalid", err: inspect.ErrInvalidRequest, diagnostic: invalidInspectUsageText},
		{name: "cancelled", err: context.Canceled, diagnostic: inspectCancelledText},
		{name: "deadline", err: context.DeadlineExceeded, diagnostic: inspectCancelledText},
		{name: "open", err: inspect.ErrOpen, diagnostic: inspectOpenFailureText},
		{name: "unsupported", err: inspect.ErrUnsupported, diagnostic: inspectUnsupportedText},
		{name: "corrupt", err: inspect.ErrCorrupt, diagnostic: inspectCorruptText},
		{name: "lookup", err: inspect.ErrLookup, diagnostic: inspectLookupText},
		{name: "artifact profile", err: artifactprofile.ErrInvalidRecord, diagnostic: inspectLookupText},
		{name: "normalization", err: source.ErrInvalidSubdivision, diagnostic: inspectLookupText},
		{name: "close", err: inspect.ErrClose, diagnostic: inspectCloseText},
		{name: "generic", err: errors.New("/private/path offset 42"), diagnostic: inspectFailureText},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, stdout, stderr := runCLIWithInspectOperations(
				t,
				t.Context(),
				[]string{"inspect", "--database", "/private/database.mmdb"},
				&bytes.Buffer{},
				&bytes.Buffer{},
				inspectOperations{execute: func(context.Context, inspect.Request) (inspect.Result, error) {
					return inspect.Result{}, test.err
				}},
			)
			if status != exitFailure || stdout != "" || stderr != test.diagnostic {
				t.Errorf("run() = %d/%q/%q, want failure/empty/%q", status, stdout, stderr, test.diagnostic)
			}
			if strings.Contains(stderr, "/private") || strings.Contains(stderr, "offset") {
				t.Errorf("stderr leaked detail: %q", stderr)
			}
		})
	}
}

func TestRunInspect_OutputFailure(t *testing.T) {
	status, stdout, stderr := runCLIWithInspectOperations(
		t,
		t.Context(),
		[]string{"inspect", "--database", "generated.mmdb"},
		failingWriter{},
		&bytes.Buffer{},
		inspectOperations{execute: func(context.Context, inspect.Request) (inspect.Result, error) {
			result := validInspectResultFixture()
			result.Lookups = nil
			return result, nil
		}},
	)
	if status != exitFailure || stdout != "" || stderr != inspectOutputText {
		t.Errorf("run() = %d/%q/%q", status, stdout, stderr)
	}
}

func TestBuildThenInspectIntegration(t *testing.T) {
	workspaceRoot := t.TempDir()
	request := compiler.Request{
		SourcePath:    filepath.Join("..", "..", "testdata", "maxmind", "GeoIP2-City-Test.mmdb"),
		SourceID:      "integration",
		WorkspaceRoot: workspaceRoot,
		BuildEpoch:    1_700_000_123,
	}
	var candidate *compiler.Candidate
	buildOperations := buildOperations{compile: func(ctx context.Context, request compiler.Request) (buildCandidate, error) {
		var err error
		candidate, err = compiler.Compile(ctx, request)
		if candidate == nil {
			return nil, err
		}
		return candidate, err
	}}
	var buildStdout bytes.Buffer
	var stderr bytes.Buffer
	status := runWithOperations(t.Context(), buildArgs(request), &buildStdout, &stderr, "ignored", buildOperations, defaultInspectOperations())
	if status != exitSuccess || stderr.Len() != 0 || candidate == nil {
		t.Fatalf("build = %d/%q/%q/candidate=%v", status, buildStdout.String(), stderr.String(), candidate)
	}
	t.Cleanup(func() {
		if candidate != nil {
			if err := candidate.Cleanup(); err != nil {
				t.Errorf("Cleanup() error = %v", err)
			}
		}
	})
	if !strings.Contains(buildStdout.String(), "candidate_path="+candidate.Path()+"\n") {
		t.Errorf("build output does not identify candidate: %q", buildStdout.String())
	}

	for _, address := range []string{"", "2.2.3.1", "2001:480:10::1", "203.0.113.1"} {
		args := []string{"inspect", "--database", candidate.Path()}
		if address != "" {
			args = append(args, "--ip", address)
		}
		var stdout bytes.Buffer
		stderr.Reset()
		status = runWithOperations(t.Context(), args, &stdout, &stderr, "ignored", defaultBuildOperations(), defaultInspectOperations())
		if status != exitSuccess || stderr.Len() != 0 || !strings.Contains(stdout.String(), "database_type="+mmdb.DatabaseType+"\n") {
			t.Fatalf("inspect %q = %d/%q/%q", address, status, stdout.String(), stderr.String())
		}
	}

	for _, command := range []string{"compare", "verify"} {
		var stdout bytes.Buffer
		stderr.Reset()
		status = runWithOperations(t.Context(), []string{command}, &stdout, &stderr, "ignored", defaultBuildOperations(), defaultInspectOperations())
		if status != exitFailure || stdout.Len() != 0 || stderr.String() != "stategeodb: "+command+" is not implemented in this build\n" {
			t.Errorf("%s availability = %d/%q/%q", command, status, stdout.String(), stderr.String())
		}
	}

	if err := candidate.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	candidate = nil
}

func validInspectResultFixture() inspect.Result {
	return inspect.Result{
		Metadata: inspect.Metadata{
			DatabaseType:      mmdb.DatabaseType,
			SchemaVersion:     mmdb.SchemaVersion,
			BuildEpoch:        1_700_000_123,
			BinaryFormatMajor: 2,
			BinaryFormatMinor: 0,
			IPVersion:         6,
			RecordSize:        mmdb.RecordSize,
			NodeCount:         9,
		},
		Lookups: []inspect.Lookup{
			{Address: netip.MustParseAddr("192.0.2.1"), Found: true, Prefix: netip.MustParsePrefix("192.0.2.0/24"), Country: "US", Subdivision: "CA"},
			{Address: netip.MustParseAddr("203.0.113.1")},
		},
	}
}

func runCLIWithInspectOperations(
	t *testing.T,
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	operations inspectOperations,
) (int, string, string) {
	t.Helper()
	status := runWithOperations(ctx, args, stdout, stderr, "ignored", defaultBuildOperations(), operations)
	return status, writerString(stdout), writerString(stderr)
}
