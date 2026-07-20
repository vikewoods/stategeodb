// Package cli implements the testable stategeodb command-line boundary.
package cli

import (
	"context"
	"io"
)

const (
	exitSuccess = 0
	exitFailure = 1

	helpText = `stategeodb is an offline geolocation database compiler.

Usage:
  stategeodb
  stategeodb --help
  stategeodb -h
  stategeodb help
  stategeodb --version
  stategeodb help <command>
  stategeodb <command> --help
  stategeodb <command> -h

Commands:
  build     Compile one local City MMDB into a verified candidate artifact
  compare   Report source coverage and disagreement without merging or publishing
  verify    Validate source or generated artifacts and configured quality gates
  inspect   Print bounded metadata and explicitly selected lookups
  publish   Publish an already built and verified candidate artifact

The build, inspect, and publish commands are operational. Compare and verify remain unavailable.
`
	buildHelp = `stategeodb build - compile one local City MMDB into a verified candidate artifact.

Usage:
  stategeodb help build
  stategeodb build --help
  stategeodb build -h
  stategeodb build --source <path> --source-id <id> --workspace-root <path> --build-epoch <unix-seconds>

Required flags:
  --source <path>           Local GeoLite2 City or GeoIP2 City MMDB
  --source-id <id>          Logical source identifier
  --workspace-root <path>   Existing absolute directory for candidate workspaces
  --build-epoch <seconds>   Positive Unix timestamp encoded into the candidate

Build writes and verifies a candidate but does not publish or replace a database.
Use the separate publish command to publish an already verified candidate.
`
	compareHelp = `stategeodb compare - report source coverage and disagreement without merging or publishing.

Usage:
  stategeodb help compare
  stategeodb compare --help
  stategeodb compare -h

Compare will report source coverage and disagreement without merging or publishing.

The compare operation is not implemented in this build.
`
	verifyHelp = `stategeodb verify - validate source or generated artifacts and configured quality gates.

Usage:
  stategeodb help verify
  stategeodb verify --help
  stategeodb verify -h

Verify will validate source or generated artifacts and configured quality gates.

The verify operation is not implemented in this build.
`
	inspectHelp = `stategeodb inspect - print bounded metadata and explicitly selected lookups.

Usage:
  stategeodb help inspect
  stategeodb inspect --help
  stategeodb inspect -h
  stategeodb inspect --database <path> [--ip <address>]...

Required flags:
  --database <path>   Generated stategeodb MMDB artifact

Optional flags:
  --ip <address>      Explicit IP lookup; repeat up to 32 times

Inspect accepts only generated stategeodb artifacts. With no --ip flags it
prints metadata only. It never prints or dumps the complete database.
`
	publishHelp = `stategeodb publish - publish an already built and verified candidate artifact.

Usage:
  stategeodb help publish
  stategeodb publish --help
  stategeodb publish -h
  stategeodb publish --candidate <path> --destination <path>

Required flags:
  --candidate <path>     Generated stategeodb candidate artifact
  --destination <path>   Explicit local stable artifact path

Publish reverifies the candidate snapshot. Identical destination bytes are left
unchanged; different bytes are installed with an atomic sibling rename. No
backup or rollback is created, and the candidate is never deleted. Only local
macOS and Linux files are supported. Publish does not compile sources.
`
	unknownArgumentText = "stategeodb: invalid command usage; run 'stategeodb --help' for usage\n"
	outputFailureText   = "stategeodb: failed to write output\n"
)

type command struct {
	name          string
	help          string
	isOperational bool
}

// Run executes the root CLI behavior with caller-owned arguments, streams, and
// version metadata. The context is propagated to operational commands so their
// blocking work can honor caller cancellation.
func Run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	version string,
) int {
	return runWithAllOperations(
		ctx,
		args,
		stdout,
		stderr,
		version,
		defaultBuildOperations(),
		defaultInspectOperations(),
		defaultPublishOperations(),
	)
}

func run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	version string,
	buildOperations buildOperations,
) int {
	return runWithOperations(
		ctx,
		args,
		stdout,
		stderr,
		version,
		buildOperations,
		defaultInspectOperations(),
	)
}

func runWithOperations(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	version string,
	buildOperations buildOperations,
	inspectOperations inspectOperations,
) int {
	return runWithAllOperations(
		ctx,
		args,
		stdout,
		stderr,
		version,
		buildOperations,
		inspectOperations,
		defaultPublishOperations(),
	)
}

func runWithAllOperations(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	version string,
	buildOperations buildOperations,
	inspectOperations inspectOperations,
	publishOperations publishOperations,
) int {
	if len(args) == 0 {
		return writeResult(stdout, stderr, helpText)
	}

	switch args[0] {
	case "--help", "-h":
		if len(args) != 1 {
			return writeInvalidUsage(stderr)
		}
		return writeResult(stdout, stderr, helpText)
	case "--version":
		if len(args) != 1 {
			return writeInvalidUsage(stderr)
		}
		return writeResult(stdout, stderr, "stategeodb "+version+"\n")
	case "help":
		return runHelp(args[1:], stdout, stderr)
	}

	cmd, ok := findCommand(args[0])
	if !ok {
		return writeInvalidUsage(stderr)
	}
	if len(args) == 2 && isHelpFlag(args[1]) {
		return writeResult(stdout, stderr, cmd.help)
	}
	if cmd.isOperational {
		switch cmd.name {
		case "build":
			return runBuild(ctx, args[1:], stdout, stderr, buildOperations)
		case "inspect":
			return runInspect(ctx, args[1:], stdout, stderr, inspectOperations)
		case "publish":
			return runPublish(ctx, args[1:], stdout, stderr, publishOperations)
		}
	}
	if len(args) == 1 {
		return writeUnavailable(stderr, cmd.name)
	}
	return writeInvalidUsage(stderr)
}

func runHelp(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		return writeResult(stdout, stderr, helpText)
	}
	if len(args) != 1 {
		return writeInvalidUsage(stderr)
	}

	cmd, ok := findCommand(args[0])
	if !ok {
		return writeInvalidUsage(stderr)
	}
	return writeResult(stdout, stderr, cmd.help)
}

func findCommand(name string) (command, bool) {
	switch name {
	case "build":
		return command{name: name, help: buildHelp, isOperational: true}, true
	case "compare":
		return command{name: name, help: compareHelp}, true
	case "verify":
		return command{name: name, help: verifyHelp}, true
	case "inspect":
		return command{name: name, help: inspectHelp, isOperational: true}, true
	case "publish":
		return command{name: name, help: publishHelp, isOperational: true}, true
	default:
		return command{}, false
	}
}

func isHelpFlag(arg string) bool {
	return arg == "--help" || arg == "-h"
}

func writeResult(stdout io.Writer, stderr io.Writer, result string) int {
	if writeString(stdout, result) {
		return exitSuccess
	}

	writeDiagnostic(stderr, outputFailureText)
	return exitFailure
}

func writeUnavailable(stderr io.Writer, name string) int {
	writeDiagnostic(stderr, "stategeodb: "+name+" is not implemented in this build\n")
	return exitFailure
}

func writeInvalidUsage(stderr io.Writer) int {
	writeDiagnostic(stderr, unknownArgumentText)
	return exitFailure
}

func writeDiagnostic(stderr io.Writer, diagnostic string) {
	if stderr == nil {
		return
	}
	_, _ = io.WriteString(stderr, diagnostic)
}
