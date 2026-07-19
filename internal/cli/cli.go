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

	helpText = `stategeodb is the command-line foundation for an offline geolocation database compiler.

Usage:
  stategeodb
  stategeodb --help
  stategeodb -h
  stategeodb help
  stategeodb --version

This foundation supports root help and version output only.
`
	unknownArgumentText = "stategeodb: unknown root argument; run 'stategeodb --help' for usage\n"
	outputFailureText   = "stategeodb: failed to write output\n"
)

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
	if len(args) != 1 {
		return writeUsageFailure(stderr)
	}

	switch args[0] {
	case "--help", "-h", "help":
		return writeResult(stdout, stderr, helpText)
	case "--version":
		return writeResult(stdout, stderr, "stategeodb "+version+"\n")
	default:
		return writeUsageFailure(stderr)
	}
}

func writeResult(stdout io.Writer, stderr io.Writer, result string) int {
	if stdout != nil {
		if _, err := io.WriteString(stdout, result); err == nil {
			return exitSuccess
		}
	}

	if stderr != nil {
		if _, err := io.WriteString(stderr, outputFailureText); err != nil {
			return exitFailure
		}
	}

	return exitFailure
}

func writeUsageFailure(stderr io.Writer) int {
	if stderr == nil {
		return exitUsage
	}
	if _, err := io.WriteString(stderr, unknownArgumentText); err != nil {
		return exitUsage
	}

	return exitUsage
}
