package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vikewoods/stategeodb/internal/artifact"
	"github.com/vikewoods/stategeodb/internal/compiler"
	"github.com/vikewoods/stategeodb/internal/inspect"
	"github.com/vikewoods/stategeodb/internal/mmdb"
	"github.com/vikewoods/stategeodb/internal/publish"
	"github.com/vikewoods/stategeodb/internal/source"
)

const publishTestEpoch int64 = 1_700_000_654

func TestParsePublishArguments(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		expected publish.Request
	}{
		{
			name:     "candidate then destination",
			args:     []string{"--candidate", "candidate.mmdb", "--destination", "stable.mmdb"},
			expected: publish.Request{CandidatePath: "candidate.mmdb", DestinationPath: "stable.mmdb"},
		},
		{
			name:     "destination then candidate",
			args:     []string{"--destination=stable.mmdb", "--candidate=candidate.mmdb"},
			expected: publish.Request{CandidatePath: "candidate.mmdb", DestinationPath: "stable.mmdb"},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request, ok := parsePublishArguments(test.args)
			if !ok || request != test.expected {
				t.Errorf("parsePublishArguments() = %#v/%t, want %#v/true", request, ok, test.expected)
			}
		})
	}

	invalid := [][]string{
		{},
		{"--candidate", "candidate.mmdb"},
		{"--destination", "stable.mmdb"},
		{"--candidate", "", "--destination", "stable.mmdb"},
		{"--candidate", "candidate.mmdb", "--destination", ""},
		{"--candidate", "one", "--candidate", "two", "--destination", "stable.mmdb"},
		{"--candidate", "candidate.mmdb", "--destination", "one", "--destination", "two"},
		{"--candidate", "candidate.mmdb", "--destination", "stable.mmdb", "extra"},
		{"--candidate", "candidate.mmdb", "--destination", "stable.mmdb", "--unknown"},
		{"--candidate", "candidate.mmdb", "--destination", "stable.mmdb", "--record-size", "28"},
		{"--candidate", "candidate.mmdb", "--destination", "stable.mmdb", "--"},
		{"--candidate", "candidate.mmdb", "--destination", "stable\nforged.mmdb"},
	}
	for _, args := range invalid {
		if request, ok := parsePublishArguments(args); ok {
			t.Errorf("parsePublishArguments(%q) = %#v/true, want false", args, request)
		}
	}
}

func TestRunPublish_ParsingFailsBeforePublication(t *testing.T) {
	called := false
	status, stdout, stderr := runCLIWithPublishOperations(
		t,
		t.Context(),
		[]string{"publish", "--candidate", "secret-candidate"},
		&bytes.Buffer{},
		&bytes.Buffer{},
		publishOperations{execute: func(context.Context, publish.Request) (publish.Result, error) {
			called = true
			return publish.Result{}, errors.New("unexpected publication")
		}},
	)
	if status != exitFailure || stdout != "" || stderr != invalidPublishUsageText || called {
		t.Errorf("run() = %d/%q/%q/called=%t", status, stdout, stderr, called)
	}
	if strings.Contains(stderr, "secret-candidate") {
		t.Error("usage diagnostic exposed candidate")
	}
}

func TestRunPublish_ExactOutputByAction(t *testing.T) {
	digest := sha256.Sum256([]byte("candidate bytes"))
	tests := []struct {
		action  publish.Action
		changed string
	}{
		{action: publish.ActionCreated, changed: "true"},
		{action: publish.ActionReplaced, changed: "true"},
		{action: publish.ActionUnchanged, changed: "false"},
	}
	for _, test := range tests {
		t.Run(string(test.action), func(t *testing.T) {
			var captured publish.Request
			status, stdout, stderr := runCLIWithPublishOperations(
				t,
				t.Context(),
				[]string{"publish", "--destination", "/stable/stategeo.mmdb", "--candidate", "/private/candidate.mmdb"},
				&bytes.Buffer{},
				&bytes.Buffer{},
				publishOperations{execute: func(_ context.Context, request publish.Request) (publish.Result, error) {
					captured = request
					return publish.Result{Action: test.action, Size: 15, SHA256: digest}, nil
				}},
			)
			expected := "artifact_path=/stable/stategeo.mmdb\n" +
				"action=" + string(test.action) + "\n" +
				"changed=" + test.changed + "\n" +
				"size_bytes=15\n" +
				"sha256=" + formatDigest(digest) + "\n"
			if status != exitSuccess || stdout != expected || stderr != "" {
				t.Errorf("run() = %d/%q/%q, want success/%q/empty", status, stdout, stderr, expected)
			}
			if captured.CandidatePath != "/private/candidate.mmdb" || captured.DestinationPath != "/stable/stategeo.mmdb" {
				t.Errorf("captured request = %#v", captured)
			}
			if strings.Contains(stdout, captured.CandidatePath) {
				t.Error("stdout exposed candidate path")
			}
		})
	}
}

func TestRunPublish_FixedSafeDiagnostics(t *testing.T) {
	tests := []struct {
		name       string
		err        error
		diagnostic string
	}{
		{name: "invalid", err: publish.ErrInvalidRequest, diagnostic: invalidPublishUsageText},
		{name: "platform", err: publish.ErrUnsupportedPlatform, diagnostic: publishPlatformText},
		{name: "cancelled", err: context.Canceled, diagnostic: publishCancelledText},
		{name: "deadline", err: context.DeadlineExceeded, diagnostic: publishCancelledText},
		{name: "candidate", err: publish.ErrCandidate, diagnostic: publishCandidateText},
		{name: "destination", err: publish.ErrDestination, diagnostic: publishDestinationText},
		{name: "verify", err: publish.ErrVerify, diagnostic: publishVerifyText},
		{name: "artifact", err: artifact.ErrUnsupported, diagnostic: publishVerifyText},
		{name: "compare", err: publish.ErrCompare, diagnostic: publishCompareText},
		{name: "write", err: publish.ErrWrite, diagnostic: publishWriteText},
		{name: "replace", err: publish.ErrReplace, diagnostic: publishReplaceText},
		{name: "cleanup", err: publish.ErrCleanup, diagnostic: publishCleanupText},
		{name: "generic", err: errors.New("/private/candidate offset 42"), diagnostic: publishFailureText},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, stdout, stderr := runCLIWithPublishOperations(
				t,
				t.Context(),
				[]string{"publish", "--candidate", "/private/candidate.mmdb", "--destination", "/private/stable.mmdb"},
				&bytes.Buffer{},
				&bytes.Buffer{},
				publishOperations{execute: func(context.Context, publish.Request) (publish.Result, error) {
					return publish.Result{}, test.err
				}},
			)
			if status != exitFailure || stdout != "" || stderr != test.diagnostic {
				t.Errorf("run() = %d/%q/%q, want failure/empty/%q", status, stdout, stderr, test.diagnostic)
			}
			if strings.Contains(stderr, "/private") || strings.Contains(stderr, "offset") {
				t.Errorf("diagnostic leaked detail: %q", stderr)
			}
		})
	}
}

func TestRunPublish_OutputFailureLeavesCommittedArtifactAndRerunsUnchanged(t *testing.T) {
	directory := t.TempDir()
	candidate := writePublishCandidate(t, directory, "candidate.mmdb", publishTestEpoch, "US")
	destination := filepath.Join(directory, "stable.mmdb")
	args := []string{"publish", "--candidate", candidate, "--destination", destination}
	status, stdout, stderr := runCLIWithPublishOperations(
		t,
		t.Context(),
		args,
		failingWriter{},
		&bytes.Buffer{},
		defaultPublishOperations(),
	)
	if status != exitFailure || stdout != "" || stderr != publishOutputText {
		t.Errorf("first run = %d/%q/%q", status, stdout, stderr)
	}
	if _, err := inspect.Inspect(t.Context(), inspect.Request{DatabasePath: destination}); err != nil {
		t.Fatalf("committed destination inspection error = %v", err)
	}

	status, stdout, stderr = runCLIWithPublishOperations(
		t,
		t.Context(),
		args,
		&bytes.Buffer{},
		&bytes.Buffer{},
		defaultPublishOperations(),
	)
	if status != exitSuccess || stderr != "" || !strings.Contains(stdout, "action=unchanged\nchanged=false\n") {
		t.Errorf("rerun = %d/%q/%q, want unchanged success", status, stdout, stderr)
	}
}

func TestBuildInspectPublishIntegration(t *testing.T) {
	workspaceRoot := t.TempDir()
	destinationDirectory := t.TempDir()
	destination := filepath.Join(destinationDirectory, "stategeo.mmdb")
	first := buildCandidateForPublish(t, workspaceRoot, publishTestEpoch)
	second := buildCandidateForPublish(t, workspaceRoot, publishTestEpoch+1)
	t.Cleanup(func() {
		if first != nil {
			if err := first.Cleanup(); err != nil {
				t.Errorf("first Cleanup() error = %v", err)
			}
		}
		if second != nil {
			if err := second.Cleanup(); err != nil {
				t.Errorf("second Cleanup() error = %v", err)
			}
		}
	})

	assertCLIInspectSuccess(t, first.Path())
	assertCLIPublishAction(t, first.Path(), destination, publish.ActionCreated)
	assertCLIInspectSuccess(t, destination)
	assertCLIPublishAction(t, first.Path(), destination, publish.ActionUnchanged)
	assertCLIPublishAction(t, second.Path(), destination, publish.ActionReplaced)
	assertCLIInspectSuccess(t, destination)

	for _, command := range []string{"compare", "verify"} {
		status, stdout, stderr := runCLI(t, []string{command}, "ignored")
		if status != exitFailure || stdout != "" || stderr != "stategeodb: "+command+" is not implemented in this build\n" {
			t.Errorf("%s availability = %d/%q/%q", command, status, stdout, stderr)
		}
	}
	if err := first.Cleanup(); err != nil {
		t.Fatalf("first Cleanup() error = %v", err)
	}
	first = nil
	if err := second.Cleanup(); err != nil {
		t.Fatalf("second Cleanup() error = %v", err)
	}
	second = nil
}

func buildCandidateForPublish(t *testing.T, workspaceRoot string, epoch int64) *compiler.Candidate {
	t.Helper()
	request := compiler.Request{
		SourcePath:    filepath.Join("..", "..", "testdata", "maxmind", "GeoIP2-City-Test.mmdb"),
		SourceID:      "publish-integration",
		WorkspaceRoot: workspaceRoot,
		BuildEpoch:    epoch,
	}
	var candidate *compiler.Candidate
	operations := buildOperations{compile: func(ctx context.Context, request compiler.Request) (buildCandidate, error) {
		var err error
		candidate, err = compiler.Compile(ctx, request)
		if candidate == nil {
			return nil, err
		}
		return candidate, err
	}}
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := runWithAllOperations(
		t.Context(),
		buildArgs(request),
		&stdout,
		&stderr,
		"ignored",
		operations,
		defaultInspectOperations(),
		defaultPublishOperations(),
	)
	if status != exitSuccess || stderr.Len() != 0 || candidate == nil ||
		!strings.Contains(stdout.String(), "candidate_path="+candidate.Path()+"\n") {
		t.Fatalf("build = %d/%q/%q/candidate=%v", status, stdout.String(), stderr.String(), candidate)
	}
	return candidate
}

func assertCLIInspectSuccess(t *testing.T, path string) {
	t.Helper()
	status, stdout, stderr := runCLI(t, []string{"inspect", "--database", path}, "ignored")
	if status != exitSuccess || stderr != "" || !strings.Contains(stdout, "database_type="+mmdb.DatabaseType+"\n") {
		t.Fatalf("inspect = %d/%q/%q", status, stdout, stderr)
	}
}

func assertCLIPublishAction(t *testing.T, candidate, destination string, action publish.Action) {
	t.Helper()
	status, stdout, stderr := runCLI(
		t,
		[]string{"publish", "--candidate", candidate, "--destination", destination},
		"ignored",
	)
	if status != exitSuccess || stderr != "" || !strings.Contains(stdout, "action="+string(action)+"\n") {
		t.Fatalf("publish %s = %d/%q/%q", action, status, stdout, stderr)
	}
}

func writePublishCandidate(t *testing.T, directory, name string, epoch int64, country string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	record, err := source.NewRecord(netip.MustParsePrefix("192.0.2.0/24"), country, "", "test")
	if err != nil {
		_ = file.Close()
		t.Fatalf("NewRecord() error = %v", err)
	}
	if _, err := mmdb.Write(file, []source.Record{record}, mmdb.Options{BuildEpoch: epoch}); err != nil {
		_ = file.Close()
		t.Fatalf("Write() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return path
}

func formatDigest(digest [sha256.Size]byte) string {
	const hexadecimal = "0123456789abcdef"
	encoded := make([]byte, sha256.Size*2)
	for index, value := range digest {
		encoded[index*2] = hexadecimal[value>>4]
		encoded[index*2+1] = hexadecimal[value&0x0f]
	}
	return string(encoded)
}

func runCLIWithPublishOperations(
	t *testing.T,
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	operations publishOperations,
) (int, string, string) {
	t.Helper()
	status := runWithAllOperations(
		ctx,
		args,
		stdout,
		stderr,
		"ignored",
		defaultBuildOperations(),
		defaultInspectOperations(),
		operations,
	)
	return status, writerString(stdout), writerString(stderr)
}
