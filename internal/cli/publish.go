package cli

import (
	"context"
	"encoding/hex"
	"errors"
	"flag"
	"io"
	"strconv"
	"strings"

	"github.com/vikewoods/stategeodb/internal/artifact"
	"github.com/vikewoods/stategeodb/internal/publish"
)

const (
	invalidPublishUsageText = "stategeodb: invalid publish usage; run 'stategeodb publish --help' for usage\n"
	publishPlatformText     = "stategeodb: local publication is unsupported on this platform\n"
	publishCancelledText    = "stategeodb: publication cancelled before commit\n"
	publishCandidateText    = "stategeodb: publication candidate is invalid\n"
	publishDestinationText  = "stategeodb: publication destination is invalid\n"
	publishVerifyText       = "stategeodb: publication candidate verification failed\n"
	publishCompareText      = "stategeodb: publication comparison failed\n"
	publishWriteText        = "stategeodb: temporary publication write failed\n"
	publishReplaceText      = "stategeodb: atomic publication replacement failed\n"
	publishCleanupText      = "stategeodb: publication cleanup failed\n"
	publishOutputText       = "stategeodb: publication completed but result output failed\n"
	publishFailureText      = "stategeodb: publication failed\n"
)

type publishOperations struct {
	execute func(context.Context, publish.Request) (publish.Result, error)
}

func defaultPublishOperations() publishOperations {
	return publishOperations{execute: publish.Publish}
}

func runPublish(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	operations publishOperations,
) int {
	request, ok := parsePublishArguments(args)
	if !ok {
		writeDiagnostic(stderr, invalidPublishUsageText)
		return exitFailure
	}
	result, err := operations.execute(ctx, request)
	if err != nil {
		writeDiagnostic(stderr, publishDiagnostic(err))
		return exitFailure
	}
	output, ok := formatPublishOutput(request.DestinationPath, result)
	if !ok {
		writeDiagnostic(stderr, publishOutputText)
		return exitFailure
	}
	if !writeString(stdout, output) {
		writeDiagnostic(stderr, publishOutputText)
		return exitFailure
	}
	return exitSuccess
}

func parsePublishArguments(args []string) (publish.Request, bool) {
	for _, argument := range args {
		if argument == "--" {
			return publish.Request{}, false
		}
	}
	flags := flag.NewFlagSet("publish", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	var candidate singleStringValue
	var destination singleStringValue
	flags.Var(&candidate, "candidate", "")
	flags.Var(&destination, "destination", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return publish.Request{}, false
	}
	if candidate.count != 1 || destination.count != 1 ||
		candidate.value == "" || destination.value == "" ||
		strings.ContainsAny(destination.value, "\r\n") {
		return publish.Request{}, false
	}
	return publish.Request{
		CandidatePath:   candidate.value,
		DestinationPath: destination.value,
	}, true
}

func formatPublishOutput(destination string, result publish.Result) (string, bool) {
	if destination == "" || strings.ContainsAny(destination, "\r\n") ||
		result.Size <= 0 || !validPublishAction(result.Action) {
		return "", false
	}
	changed := result.Action != publish.ActionUnchanged
	output := make([]byte, 0, len(destination)+160)
	output = appendKeyValue(output, "artifact_path", destination)
	output = appendKeyValue(output, "action", string(result.Action))
	output = appendKeyValue(output, "changed", strconv.FormatBool(changed))
	output = append(output, "size_bytes="...)
	output = strconv.AppendInt(output, result.Size, 10)
	output = append(output, '\n')
	output = append(output, "sha256="...)
	output = hex.AppendEncode(output, result.SHA256[:])
	output = append(output, '\n')
	return string(output), true
}

func validPublishAction(action publish.Action) bool {
	switch action {
	case publish.ActionCreated, publish.ActionReplaced, publish.ActionUnchanged:
		return true
	default:
		return false
	}
}

func publishDiagnostic(err error) string {
	switch {
	case errors.Is(err, publish.ErrInvalidRequest):
		return invalidPublishUsageText
	case errors.Is(err, publish.ErrUnsupportedPlatform):
		return publishPlatformText
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return publishCancelledText
	case errors.Is(err, publish.ErrCleanup):
		return publishCleanupText
	case errors.Is(err, publish.ErrCandidate):
		return publishCandidateText
	case errors.Is(err, publish.ErrDestination):
		return publishDestinationText
	case errors.Is(err, publish.ErrVerify),
		errors.Is(err, artifact.ErrUnsupported),
		errors.Is(err, artifact.ErrCorrupt):
		return publishVerifyText
	case errors.Is(err, publish.ErrCompare):
		return publishCompareText
	case errors.Is(err, publish.ErrWrite):
		return publishWriteText
	case errors.Is(err, publish.ErrReplace):
		return publishReplaceText
	default:
		return publishFailureText
	}
}
