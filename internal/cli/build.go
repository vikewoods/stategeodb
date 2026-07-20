package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/vikewoods/stategeodb/internal/compiler"
	"github.com/vikewoods/stategeodb/internal/mmdb"
	"github.com/vikewoods/stategeodb/internal/source/maxmind"
)

const (
	invalidBuildUsageText    = "stategeodb: invalid build usage; run 'stategeodb build --help' for usage\n"
	buildCancelledText       = "stategeodb: build cancelled\n"
	sourceOpenText           = "stategeodb: source database could not be opened\n"
	sourceUnsupportedText    = "stategeodb: source database is unsupported\n"
	sourceCorruptText        = "stategeodb: source database is corrupt\n"
	workspaceFailureText     = "stategeodb: candidate workspace failed\n"
	writeFailureText         = "stategeodb: candidate write failed\n"
	verifyFailureText        = "stategeodb: candidate verification failed\n"
	equivalenceFailureText   = "stategeodb: candidate equivalence failed\n"
	cleanupFailureText       = "stategeodb: candidate cleanup failed\n"
	buildFailureText         = "stategeodb: build failed\n"
	buildOutputFailureText   = "stategeodb: failed to write build output; candidate removed\n"
	outputCleanupFailureText = "stategeodb: failed to write build output and clean candidate workspace\n"
)

type buildCandidate interface {
	Path() string
	InputRecordCount() int
	EquivalenceStats() compiler.EquivalenceStats
	Size() int64
	BuildEpoch() int64
	Cleanup() error
}

type buildOperations struct {
	compile func(context.Context, compiler.Request) (buildCandidate, error)
}

func defaultBuildOperations() buildOperations {
	return buildOperations{
		compile: func(ctx context.Context, request compiler.Request) (buildCandidate, error) {
			candidate, err := compiler.Compile(ctx, request)
			if candidate == nil {
				return nil, err
			}
			return candidate, err
		},
	}
}

func runBuild(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	operations buildOperations,
) int {
	request, ok := parseBuildArguments(args)
	if !ok {
		writeDiagnostic(stderr, invalidBuildUsageText)
		return exitFailure
	}

	candidate, err := operations.compile(ctx, request)
	if err != nil {
		if candidate != nil {
			if cleanupErr := candidate.Cleanup(); cleanupErr != nil {
				writeDiagnostic(stderr, cleanupFailureText)
				return exitFailure
			}
		}
		writeDiagnostic(stderr, buildDiagnostic(err))
		return exitFailure
	}
	if candidate == nil {
		writeDiagnostic(stderr, buildFailureText)
		return exitFailure
	}

	output, ok := formatBuildOutput(candidate)
	if !ok {
		return failBuildOutput(candidate, stderr)
	}
	if !writeString(stdout, output) {
		return failBuildOutput(candidate, stderr)
	}
	return exitSuccess
}

func parseBuildArguments(args []string) (compiler.Request, bool) {
	for _, arg := range args {
		if arg == "--" {
			return compiler.Request{}, false
		}
	}

	flags := flag.NewFlagSet("build", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	sourcePath := flags.String("source", "", "")
	sourceID := flags.String("source-id", "", "")
	workspaceRoot := flags.String("workspace-root", "", "")
	buildEpoch := flags.Int64("build-epoch", 0, "")

	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return compiler.Request{}, false
	}
	requiredFlags := [...]string{"source", "source-id", "workspace-root", "build-epoch"}
	present := make(map[string]bool, len(requiredFlags))
	flags.Visit(func(setFlag *flag.Flag) {
		present[setFlag.Name] = true
	})
	for _, name := range requiredFlags {
		if !present[name] {
			return compiler.Request{}, false
		}
	}
	if *sourcePath == "" || *sourceID == "" || *workspaceRoot == "" || *buildEpoch <= 0 {
		return compiler.Request{}, false
	}
	if strings.ContainsAny(*workspaceRoot, "\r\n") {
		return compiler.Request{}, false
	}

	return compiler.Request{
		SourcePath:    *sourcePath,
		SourceID:      *sourceID,
		WorkspaceRoot: *workspaceRoot,
		BuildEpoch:    *buildEpoch,
	}, true
}

func formatBuildOutput(candidate buildCandidate) (string, bool) {
	path := candidate.Path()
	if path == "" || strings.ContainsAny(path, "\r\n") {
		return "", false
	}
	stats := candidate.EquivalenceStats()
	return fmt.Sprintf(
		"candidate_path=%s\ninput_records=%d\noutput_networks=%d\ncompared_segments=%d\nsize_bytes=%d\nbuild_epoch=%d\n",
		path,
		candidate.InputRecordCount(),
		stats.OutputNetworks,
		stats.ComparedSegments,
		candidate.Size(),
		candidate.BuildEpoch(),
	), true
}

func failBuildOutput(candidate buildCandidate, stderr io.Writer) int {
	if err := candidate.Cleanup(); err != nil {
		writeDiagnostic(stderr, outputCleanupFailureText)
		return exitFailure
	}
	writeDiagnostic(stderr, buildOutputFailureText)
	return exitFailure
}

func buildDiagnostic(err error) string {
	switch {
	case errors.Is(err, compiler.ErrCleanup):
		return cleanupFailureText
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return buildCancelledText
	case errors.Is(err, maxmind.ErrOpen):
		return sourceOpenText
	case errors.Is(err, maxmind.ErrUnsupported):
		return sourceUnsupportedText
	case errors.Is(err, maxmind.ErrCorrupt):
		return sourceCorruptText
	case errors.Is(err, mmdb.ErrInvalidInput),
		errors.Is(err, mmdb.ErrBuild),
		errors.Is(err, mmdb.ErrWrite):
		return writeFailureText
	case errors.Is(err, compiler.ErrNotEquivalent):
		return equivalenceFailureText
	case errors.Is(err, compiler.ErrVerify):
		return verifyFailureText
	case errors.Is(err, compiler.ErrWorkspace):
		return workspaceFailureText
	case errors.Is(err, compiler.ErrInvalidRequest):
		return invalidBuildUsageText
	default:
		return buildFailureText
	}
}

func writeString(destination io.Writer, value string) bool {
	if destination == nil {
		return false
	}
	written, err := io.WriteString(destination, value)
	return err == nil && written == len(value)
}
