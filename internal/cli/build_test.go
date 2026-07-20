package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/vikewoods/stategeodb/internal/compiler"
	"github.com/vikewoods/stategeodb/internal/mmdb"
	"github.com/vikewoods/stategeodb/internal/source/maxmind"
)

const testBuildEpoch int64 = 1_700_000_000

func TestRun_BuildHelp(t *testing.T) {
	forms := [][]string{
		{"help", "build"},
		{"build", "--help"},
		{"build", "-h"},
	}
	for _, args := range forms {
		status, stdout, stderr := runCLI(t, args, "ignored")
		if status != exitSuccess {
			t.Errorf("Run(%q) status = %d, want %d", args, status, exitSuccess)
		}
		if stdout != buildHelp {
			t.Errorf("Run(%q) stdout = %q, want build help", args, stdout)
		}
		if stderr != "" {
			t.Errorf("Run(%q) stderr = %q, want empty", args, stderr)
		}
	}

	for _, flagName := range []string{"--source", "--source-id", "--workspace-root", "--build-epoch"} {
		if count := strings.Count(buildHelp, flagName); count < 2 {
			t.Errorf("build help contains %q %d times, want syntax and flag documentation", flagName, count)
		}
	}
	for _, content := range []string{"verified candidate", "does not publish", "separate publish command"} {
		if !strings.Contains(buildHelp, content) {
			t.Errorf("build help does not contain %q", content)
		}
	}
	for _, content := range []string{"JSON", "configuration file", "environment variable"} {
		if strings.Contains(buildHelp, content) {
			t.Errorf("build help unexpectedly documents %q", content)
		}
	}
}

func TestRun_BuildParsesRequiredFlagsInAnyOrder(t *testing.T) {
	expectedRequest := compiler.Request{
		SourcePath:    "/inputs/city.mmdb",
		SourceID:      "primary-city",
		WorkspaceRoot: "/workspaces",
		BuildEpoch:    testBuildEpoch,
	}
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "documented order",
			args: []string{
				"build",
				"--source", expectedRequest.SourcePath,
				"--source-id", expectedRequest.SourceID,
				"--workspace-root", expectedRequest.WorkspaceRoot,
				"--build-epoch", fmt.Sprint(expectedRequest.BuildEpoch),
			},
		},
		{
			name: "different order and assignment forms",
			args: []string{
				"build",
				"--build-epoch=" + fmt.Sprint(expectedRequest.BuildEpoch),
				"--workspace-root=" + expectedRequest.WorkspaceRoot,
				"--source-id=" + expectedRequest.SourceID,
				"--source=" + expectedRequest.SourcePath,
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			compileCalls := 0
			operations := buildOperations{
				compile: func(actual context.Context, request compiler.Request) (buildCandidate, error) {
					compileCalls++
					if actual != ctx {
						t.Error("build replaced the caller-provided context")
					}
					if request != expectedRequest {
						t.Errorf("compiler request = %+v, want %+v", request, expectedRequest)
					}
					return newFakeBuildCandidate(), nil
				},
			}

			status, stdout, stderr := runCLIWithBuildOperations(
				t,
				ctx,
				test.args,
				&bytes.Buffer{},
				&bytes.Buffer{},
				operations,
			)
			if status != exitSuccess {
				t.Errorf("run() status = %d, want %d", status, exitSuccess)
			}
			if stdout == "" || stderr != "" {
				t.Errorf("run() stdout/stderr = %q/%q, want output/empty", stdout, stderr)
			}
			if compileCalls != 1 {
				t.Errorf("compile calls = %d, want 1", compileCalls)
			}
		})
	}
}

func TestRun_BuildRejectsMissingRequiredFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{
			name: "source",
			args: []string{"build", "--source-id", "primary", "--workspace-root", "/work", "--build-epoch", "1"},
		},
		{
			name: "source id",
			args: []string{"build", "--source", "/source", "--workspace-root", "/work", "--build-epoch", "1"},
		},
		{
			name: "workspace root",
			args: []string{"build", "--source", "/source", "--source-id", "primary", "--build-epoch", "1"},
		},
		{
			name: "build epoch",
			args: []string{"build", "--source", "/source", "--source-id", "primary", "--workspace-root", "/work"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertBuildUsageFailureWithoutCompile(t, test.args)
		})
	}
}

func TestRun_BuildDoesNotReadEnvironmentFallbacks(t *testing.T) {
	t.Setenv("STATEGEODB_SOURCE", "/secret/source.mmdb")
	t.Setenv("STATEGEODB_SOURCE_ID", "environment-source")
	t.Setenv("STATEGEODB_WORKSPACE_ROOT", "/secret/workspace")
	t.Setenv("STATEGEODB_BUILD_EPOCH", "999")
	assertBuildUsageFailureWithoutCompile(t, []string{"build"})
}

func TestRun_BuildRejectsInvalidArguments(t *testing.T) {
	valid := []string{
		"build",
		"--source", "/source",
		"--source-id", "primary",
		"--workspace-root", "/work",
		"--build-epoch", "1",
	}
	tests := []struct {
		name      string
		args      []string
		sensitive string
	}{
		{name: "empty source", args: replaceBuildFlag(valid, "--source", "")},
		{name: "empty source id", args: replaceBuildFlag(valid, "--source-id", "")},
		{name: "empty workspace root", args: replaceBuildFlag(valid, "--workspace-root", "")},
		{name: "empty build epoch", args: replaceBuildFlag(valid, "--build-epoch", "")},
		{name: "invalid build epoch", args: replaceBuildFlag(valid, "--build-epoch", "secret-epoch"), sensitive: "secret-epoch"},
		{name: "zero build epoch", args: replaceBuildFlag(valid, "--build-epoch", "0")},
		{name: "negative build epoch", args: replaceBuildFlag(valid, "--build-epoch", "-1")},
		{name: "positional argument", args: appendCopy(valid, "extra")},
		{name: "unknown flag", args: appendCopy(valid, "--token=top-secret"), sensitive: "top-secret"},
		{name: "record size flag", args: appendCopy(valid, "--record-size=28")},
		{name: "profile flag", args: appendCopy(valid, "--profile=compliance")},
		{name: "argument separator", args: appendCopy(valid, "--")},
		{name: "help with extra argument", args: []string{"build", "--help", "top-secret"}, sensitive: "top-secret"},
		{name: "missing flag value", args: []string{"build", "--source"}},
		{name: "newline workspace", args: replaceBuildFlag(valid, "--workspace-root", "/work\nforged=value"), sensitive: "forged=value"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, stdout, stderr := assertBuildUsageFailureWithoutCompile(t, test.args)
			if status != exitFailure || stdout != "" || stderr != invalidBuildUsageText {
				t.Errorf("usage result = %d/%q/%q", status, stdout, stderr)
			}
			if test.sensitive != "" && strings.Contains(stderr, test.sensitive) {
				t.Errorf("stderr exposed invalid value %q", test.sensitive)
			}
		})
	}
}

func TestRun_BuildSuccessWithCityFixture(t *testing.T) {
	workspaceRoot := t.TempDir()
	request := compiler.Request{
		SourcePath: filepath.Join(
			"..",
			"..",
			"testdata",
			"maxmind",
			"GeoIP2-City-Test.mmdb",
		),
		SourceID:      "licensed-city",
		WorkspaceRoot: workspaceRoot,
		BuildEpoch:    testBuildEpoch,
	}
	args := buildArgs(request)
	operations := defaultBuildOperations()
	compile := operations.compile
	var returned *compiler.Candidate
	operations.compile = func(ctx context.Context, request compiler.Request) (buildCandidate, error) {
		candidate, err := compile(ctx, request)
		if candidate != nil {
			returned = candidate.(*compiler.Candidate)
		}
		return candidate, err
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := run(t.Context(), args, &stdout, &stderr, "ignored", operations)
	if status != exitSuccess {
		t.Fatalf("run() status = %d, stderr = %q", status, stderr.String())
	}
	if stderr.Len() != 0 {
		t.Errorf("run() stderr = %q, want empty", stderr.String())
	}
	if returned == nil {
		t.Fatal("compiler returned no candidate")
	}

	stats := returned.EquivalenceStats()
	expectedOutput := fmt.Sprintf(
		"candidate_path=%s\ninput_records=%d\noutput_networks=%d\ncompared_segments=%d\nsize_bytes=%d\nbuild_epoch=%d\n",
		returned.Path(),
		returned.InputRecordCount(),
		stats.OutputNetworks,
		stats.ComparedSegments,
		returned.Size(),
		returned.BuildEpoch(),
	)
	if stdout.String() != expectedOutput {
		t.Errorf("run() stdout = %q, want %q", stdout.String(), expectedOutput)
	}
	for _, key := range []string{
		"candidate_path",
		"input_records",
		"output_networks",
		"compared_segments",
		"size_bytes",
		"build_epoch",
	} {
		if count := strings.Count(stdout.String(), key+"="); count != 1 {
			t.Errorf("stdout contains key %q %d times, want once", key, count)
		}
	}
	for _, content := range []string{"published", "publication", request.SourceID, request.SourcePath} {
		if strings.Contains(stdout.String(), content) {
			t.Errorf("stdout unexpectedly contains %q", content)
		}
	}

	candidatePath := returned.Path()
	if _, err := os.Stat(candidatePath); err != nil {
		t.Fatalf("candidate after run: %v", err)
	}
	reader, err := maxminddb.Open(candidatePath)
	if err != nil {
		t.Fatalf("open candidate: %v", err)
	}
	if err := reader.Verify(); err != nil {
		_ = reader.Close()
		t.Fatalf("verify candidate: %v", err)
	}
	if err := reader.Close(); err != nil {
		t.Fatalf("close candidate: %v", err)
	}
	if err := returned.Cleanup(); err != nil {
		t.Fatalf("candidate Cleanup() error = %v", err)
	}
	if _, err := os.Lstat(candidatePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("candidate after cleanup error = %v, want os.ErrNotExist", err)
	}
}

func TestRun_BuildWritesSuccessfulOutputOnce(t *testing.T) {
	operations := successfulBuildOperations(newFakeBuildCandidate())
	var stdout countingWriter
	var stderr bytes.Buffer
	status := run(
		t.Context(),
		buildArgs(fakeBuildRequest()),
		&stdout,
		&stderr,
		"ignored",
		operations,
	)
	if status != exitSuccess {
		t.Errorf("run() status = %d, want %d", status, exitSuccess)
	}
	if stdout.calls != 1 {
		t.Errorf("stdout writes = %d, want 1", stdout.calls)
	}
	if stderr.Len() != 0 {
		t.Errorf("stderr = %q, want empty", stderr.String())
	}
}

func TestRun_BuildRepeatedInvocationsDoNotLeakFlags(t *testing.T) {
	requests := []compiler.Request{
		{
			SourcePath:    "/inputs/first.mmdb",
			SourceID:      "first",
			WorkspaceRoot: "/workspaces/first",
			BuildEpoch:    1,
		},
		{
			SourcePath:    "/inputs/second.mmdb",
			SourceID:      "second",
			WorkspaceRoot: "/workspaces/second",
			BuildEpoch:    2,
		},
	}
	var compiled []compiler.Request
	operations := buildOperations{
		compile: func(_ context.Context, request compiler.Request) (buildCandidate, error) {
			compiled = append(compiled, request)
			candidate := newFakeBuildCandidate()
			candidate.path = filepath.Join(request.WorkspaceRoot, "candidate.mmdb")
			candidate.epoch = request.BuildEpoch
			return candidate, nil
		},
	}
	for _, request := range requests {
		var stdout bytes.Buffer
		var stderr bytes.Buffer
		status := run(
			t.Context(),
			buildArgs(request),
			&stdout,
			&stderr,
			"ignored",
			operations,
		)
		if status != exitSuccess || stderr.Len() != 0 {
			t.Fatalf("run() result = %d/%q", status, stderr.String())
		}
		if !strings.Contains(stdout.String(), "candidate_path="+request.WorkspaceRoot) ||
			!strings.Contains(stdout.String(), fmt.Sprintf("build_epoch=%d\n", request.BuildEpoch)) {
			t.Errorf("run() stdout = %q, want current invocation values", stdout.String())
		}
	}
	if len(compiled) != len(requests) {
		t.Fatalf("compile calls = %d, want %d", len(compiled), len(requests))
	}
	for index := range requests {
		if compiled[index] != requests[index] {
			t.Errorf("compile request %d = %+v, want %+v", index, compiled[index], requests[index])
		}
	}
}

func TestRun_BuildCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := Run(ctx, buildArgs(fakeBuildRequest()), &stdout, &stderr, "ignored")
	if status != exitFailure {
		t.Errorf("Run() status = %d, want %d", status, exitFailure)
	}
	if stdout.Len() != 0 {
		t.Errorf("Run() stdout = %q, want empty", stdout.String())
	}
	if stderr.String() != buildCancelledText {
		t.Errorf("Run() stderr = %q, want %q", stderr.String(), buildCancelledText)
	}
}

func TestRun_BuildMapsCompilerFailures(t *testing.T) {
	tests := []struct {
		name       string
		cause      error
		diagnostic string
	}{
		{name: "invalid request", cause: compiler.ErrInvalidRequest, diagnostic: invalidBuildUsageText},
		{name: "cancelled", cause: context.Canceled, diagnostic: buildCancelledText},
		{name: "deadline", cause: context.DeadlineExceeded, diagnostic: buildCancelledText},
		{name: "source open", cause: maxmind.ErrOpen, diagnostic: sourceOpenText},
		{name: "source unsupported", cause: maxmind.ErrUnsupported, diagnostic: sourceUnsupportedText},
		{name: "source corrupt", cause: maxmind.ErrCorrupt, diagnostic: sourceCorruptText},
		{name: "artifact profile", cause: compiler.ErrProfile, diagnostic: profileFailureText},
		{name: "workspace", cause: compiler.ErrWorkspace, diagnostic: workspaceFailureText},
		{name: "writer input", cause: mmdb.ErrInvalidInput, diagnostic: writeFailureText},
		{name: "writer build", cause: mmdb.ErrBuild, diagnostic: writeFailureText},
		{name: "writer output", cause: mmdb.ErrWrite, diagnostic: writeFailureText},
		{name: "verification", cause: compiler.ErrVerify, diagnostic: verifyFailureText},
		{name: "equivalence", cause: compiler.ErrNotEquivalent, diagnostic: equivalenceFailureText},
		{name: "cleanup", cause: compiler.ErrCleanup, diagnostic: cleanupFailureText},
		{name: "generic", cause: errors.New("generic unsafe failure"), diagnostic: buildFailureText},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			unsafe := fmt.Errorf("unsafe path and parser offset: %w", test.cause)
			operations := buildOperations{
				compile: func(context.Context, compiler.Request) (buildCandidate, error) {
					return nil, unsafe
				},
			}
			status, stdout, stderr := runCLIWithBuildOperations(
				t,
				t.Context(),
				buildArgs(fakeBuildRequest()),
				&bytes.Buffer{},
				&bytes.Buffer{},
				operations,
			)
			if status != exitFailure {
				t.Errorf("run() status = %d, want %d", status, exitFailure)
			}
			if stdout != "" {
				t.Errorf("run() stdout = %q, want empty", stdout)
			}
			if stderr != test.diagnostic {
				t.Errorf("run() stderr = %q, want %q", stderr, test.diagnostic)
			}
			if strings.Contains(stderr, "unsafe") || strings.Contains(stderr, "offset") {
				t.Errorf("run() stderr exposed underlying error: %q", stderr)
			}
		})
	}
}

func TestRun_BuildOutputFailureCleansCompiledCandidate(t *testing.T) {
	request := compiler.Request{
		SourcePath:    filepath.Join("..", "..", "testdata", "maxmind", "GeoIP2-City-Test.mmdb"),
		SourceID:      "licensed-city",
		WorkspaceRoot: t.TempDir(),
		BuildEpoch:    testBuildEpoch,
	}
	operations := defaultBuildOperations()
	compile := operations.compile
	var returned *compiler.Candidate
	operations.compile = func(ctx context.Context, request compiler.Request) (buildCandidate, error) {
		candidate, err := compile(ctx, request)
		if candidate != nil {
			returned = candidate.(*compiler.Candidate)
		}
		return candidate, err
	}

	var stderr bytes.Buffer
	status := run(
		t.Context(),
		buildArgs(request),
		failingWriter{},
		&stderr,
		"ignored",
		operations,
	)
	if status != exitFailure {
		t.Errorf("run() status = %d, want %d", status, exitFailure)
	}
	if stderr.String() != buildOutputFailureText {
		t.Errorf("run() stderr = %q, want %q", stderr.String(), buildOutputFailureText)
	}
	if returned == nil {
		t.Fatal("compiler returned no candidate")
	}
	if _, err := os.Lstat(returned.Path()); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("candidate after output failure error = %v, want os.ErrNotExist", err)
	}
}

func TestRun_BuildOutputAndCleanupFailuresRemainFailure(t *testing.T) {
	candidate := newFakeBuildCandidate()
	candidate.cleanupErr = errors.New("unsafe cleanup path")
	operations := successfulBuildOperations(candidate)
	var stderr bytes.Buffer
	status := run(
		t.Context(),
		buildArgs(fakeBuildRequest()),
		failingWriter{},
		&stderr,
		"ignored",
		operations,
	)
	if status != exitFailure {
		t.Errorf("run() status = %d, want %d", status, exitFailure)
	}
	if candidate.cleanupCalls != 1 {
		t.Errorf("Cleanup() calls = %d, want 1", candidate.cleanupCalls)
	}
	if stderr.String() != outputCleanupFailureText {
		t.Errorf("run() stderr = %q, want %q", stderr.String(), outputCleanupFailureText)
	}
	if strings.Contains(stderr.String(), "unsafe") {
		t.Errorf("run() stderr exposed cleanup detail: %q", stderr.String())
	}

	status = run(
		t.Context(),
		buildArgs(fakeBuildRequest()),
		failingWriter{},
		failingWriter{},
		"ignored",
		successfulBuildOperations(newFakeBuildCandidate()),
	)
	if status != exitFailure {
		t.Errorf("run() with failed diagnostics status = %d, want %d", status, exitFailure)
	}
}

func TestRun_BuildUnsafeCandidatePathFailsAndCleans(t *testing.T) {
	candidate := newFakeBuildCandidate()
	candidate.path = "/workspace/candidate.mmdb\nforged=value"
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := run(
		t.Context(),
		buildArgs(fakeBuildRequest()),
		&stdout,
		&stderr,
		"ignored",
		successfulBuildOperations(candidate),
	)
	if status != exitFailure || stdout.Len() != 0 {
		t.Errorf("run() result = %d/%q, want failure/empty", status, stdout.String())
	}
	if candidate.cleanupCalls != 1 {
		t.Errorf("Cleanup() calls = %d, want 1", candidate.cleanupCalls)
	}
	if stderr.String() != buildOutputFailureText {
		t.Errorf("run() stderr = %q, want %q", stderr.String(), buildOutputFailureText)
	}
}

func assertBuildUsageFailureWithoutCompile(
	t *testing.T,
	args []string,
) (int, string, string) {
	t.Helper()
	operations := buildOperations{
		compile: func(context.Context, compiler.Request) (buildCandidate, error) {
			t.Error("compiler called after CLI parsing failure")
			return nil, errors.New("unexpected compiler call")
		},
	}
	status, stdout, stderr := runCLIWithBuildOperations(
		t,
		t.Context(),
		args,
		&bytes.Buffer{},
		&bytes.Buffer{},
		operations,
	)
	if status != exitFailure || stdout != "" || stderr != invalidBuildUsageText {
		t.Errorf("usage result = %d/%q/%q, want failure/empty/%q", status, stdout, stderr, invalidBuildUsageText)
	}
	return status, stdout, stderr
}

func runCLIWithBuildOperations(
	t *testing.T,
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	operations buildOperations,
) (int, string, string) {
	t.Helper()
	var capturedStdout bytes.Buffer
	var capturedStderr bytes.Buffer
	if stdout == nil {
		stdout = &capturedStdout
	}
	if stderr == nil {
		stderr = &capturedStderr
	}
	status := run(ctx, args, stdout, stderr, "ignored", operations)
	return status, writerString(stdout), writerString(stderr)
}

func writerString(writer io.Writer) string {
	switch value := writer.(type) {
	case *bytes.Buffer:
		return value.String()
	case *countingWriter:
		return string(value.contents)
	default:
		return ""
	}
}

func buildArgs(request compiler.Request) []string {
	return []string{
		"build",
		"--source", request.SourcePath,
		"--source-id", request.SourceID,
		"--workspace-root", request.WorkspaceRoot,
		"--build-epoch", fmt.Sprint(request.BuildEpoch),
	}
}

func fakeBuildRequest() compiler.Request {
	return compiler.Request{
		SourcePath:    "/inputs/city.mmdb",
		SourceID:      "primary-city",
		WorkspaceRoot: "/workspaces",
		BuildEpoch:    testBuildEpoch,
	}
}

func successfulBuildOperations(candidate buildCandidate) buildOperations {
	return buildOperations{
		compile: func(context.Context, compiler.Request) (buildCandidate, error) {
			return candidate, nil
		},
	}
}

func replaceBuildFlag(args []string, name string, value string) []string {
	replaced := append([]string(nil), args...)
	for index := range len(replaced) - 1 {
		if replaced[index] == name {
			replaced[index+1] = value
			return replaced
		}
	}
	return replaced
}

func appendCopy(values []string, additions ...string) []string {
	return append(append([]string(nil), values...), additions...)
}

type fakeBuildCandidate struct {
	path         string
	inputRecords int
	stats        compiler.EquivalenceStats
	size         int64
	epoch        int64
	cleanupErr   error
	cleanupCalls int
}

func newFakeBuildCandidate() *fakeBuildCandidate {
	return &fakeBuildCandidate{
		path:         "/workspaces/generated/candidate.mmdb",
		inputRecords: 4,
		stats: compiler.EquivalenceStats{
			SourceRecords:    4,
			OutputNetworks:   3,
			ComparedSegments: 5,
		},
		size:  4096,
		epoch: testBuildEpoch,
	}
}

func (candidate *fakeBuildCandidate) Path() string {
	return candidate.path
}

func (candidate *fakeBuildCandidate) InputRecordCount() int {
	return candidate.inputRecords
}

func (candidate *fakeBuildCandidate) EquivalenceStats() compiler.EquivalenceStats {
	return candidate.stats
}

func (candidate *fakeBuildCandidate) Size() int64 {
	return candidate.size
}

func (candidate *fakeBuildCandidate) BuildEpoch() int64 {
	return candidate.epoch
}

func (candidate *fakeBuildCandidate) Cleanup() error {
	candidate.cleanupCalls++
	return candidate.cleanupErr
}

type countingWriter struct {
	contents []byte
	calls    int
}

func (writer *countingWriter) Write(value []byte) (int, error) {
	writer.calls++
	writer.contents = append(writer.contents, value...)
	return len(value), nil
}
