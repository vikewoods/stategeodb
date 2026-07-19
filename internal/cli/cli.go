// Package cli implements the testable stategeodb command-line boundary.
package cli

import (
	"context"
	"io"
)

const (
	exitSuccess = 0
	exitFailure = 1
	exitUsage   = 2

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
  build     Compile local sources and overrides into a verified candidate artifact
  compare   Report source coverage and disagreement without merging or publishing
  verify    Validate source or generated artifacts and configured quality gates
  inspect   Print bounded metadata and explicitly selected lookups
  publish   Publish an already built and verified candidate artifact

Domain operations are not implemented in this build.
`
	buildHelp = `stategeodb build - compile local sources and overrides into a verified candidate artifact.

Usage:
  stategeodb help build
  stategeodb build --help
  stategeodb build -h

Build will compile configured local sources and overrides into a verified candidate artifact.
It will not publish or replace the stable artifact.

The build operation is not implemented in this build.
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

Inspect will print bounded metadata and explicitly selected lookups for diagnostics.
It will not dump a complete dataset by default.

The inspect operation is not implemented in this build.
`
	publishHelp = `stategeodb publish - publish an already built and verified candidate artifact.

Usage:
  stategeodb help publish
  stategeodb publish --help
  stategeodb publish -h

Publish will publish an already built and verified candidate through the explicit publication boundary.
It will not compile sources.

The publish operation is not implemented in this build.
`
	unknownArgumentText = "stategeodb: invalid command usage; run 'stategeodb --help' for usage\n"
	outputFailureText   = "stategeodb: failed to write output\n"
)

type command struct {
	name string
	help string
}

// Run executes the root CLI behavior with caller-owned arguments, streams, and
// version metadata. The context is carried into this boundary so later blocking
// commands can honor cancellation without changing its signature.
func Run(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	version string,
) int {
	if len(args) == 0 {
		return writeResult(stdout, stderr, helpText)
	}

	switch args[0] {
	case "--help", "-h":
		if len(args) != 1 {
			return writeUsageFailure(stderr)
		}
		return writeResult(stdout, stderr, helpText)
	case "--version":
		if len(args) != 1 {
			return writeUsageFailure(stderr)
		}
		return writeResult(stdout, stderr, "stategeodb "+version+"\n")
	case "help":
		return runHelp(args[1:], stdout, stderr)
	}

	cmd, ok := findCommand(args[0])
	if !ok {
		return writeUsageFailure(stderr)
	}
	if len(args) == 1 {
		return writeUnavailable(stderr, cmd.name)
	}
	if len(args) == 2 && isHelpFlag(args[1]) {
		return writeResult(stdout, stderr, cmd.help)
	}
	return writeUsageFailure(stderr)
}

func runHelp(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) == 0 {
		return writeResult(stdout, stderr, helpText)
	}
	if len(args) != 1 {
		return writeUsageFailure(stderr)
	}

	cmd, ok := findCommand(args[0])
	if !ok {
		return writeUsageFailure(stderr)
	}
	return writeResult(stdout, stderr, cmd.help)
}

func findCommand(name string) (command, bool) {
	switch name {
	case "build":
		return command{name: name, help: buildHelp}, true
	case "compare":
		return command{name: name, help: compareHelp}, true
	case "verify":
		return command{name: name, help: verifyHelp}, true
	case "inspect":
		return command{name: name, help: inspectHelp}, true
	case "publish":
		return command{name: name, help: publishHelp}, true
	default:
		return command{}, false
	}
}

func isHelpFlag(arg string) bool {
	return arg == "--help" || arg == "-h"
}

func writeResult(stdout io.Writer, stderr io.Writer, result string) int {
	if stdout != nil {
		if _, err := io.WriteString(stdout, result); err == nil {
			return exitSuccess
		}
	}

	writeDiagnostic(stderr, outputFailureText)
	return exitFailure
}

func writeUnavailable(stderr io.Writer, name string) int {
	writeDiagnostic(stderr, "stategeodb: "+name+" is not implemented in this build\n")
	return exitFailure
}

func writeUsageFailure(stderr io.Writer) int {
	writeDiagnostic(stderr, unknownArgumentText)
	return exitUsage
}

func writeDiagnostic(stderr io.Writer, diagnostic string) {
	if stderr == nil {
		return
	}
	_, _ = io.WriteString(stderr, diagnostic)
}
