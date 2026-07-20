package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

var testCommands = []command{
	{name: "build", help: buildHelp, isOperational: true},
	{name: "compare", help: compareHelp},
	{name: "verify", help: verifyHelp},
	{name: "inspect", help: inspectHelp, isOperational: true},
	{name: "publish", help: publishHelp, isOperational: true},
}

func TestRun_RootHelpForms(t *testing.T) {
	expectedStatus, expectedHelp, expectedStderr := runCLI(t, nil, "ignored")
	if expectedStatus != exitSuccess {
		t.Fatalf("Run(nil) status = %d, want %d", expectedStatus, exitSuccess)
	}
	if expectedStderr != "" {
		t.Fatalf("Run(nil) stderr = %q, want empty", expectedStderr)
	}

	stableContent := []string{
		"stategeodb is an offline geolocation database compiler",
		"Usage:\n",
		"stategeodb --help",
		"stategeodb -h",
		"stategeodb help",
		"stategeodb --version",
		"The build, inspect, and publish commands are operational.",
	}
	for _, content := range stableContent {
		if !strings.Contains(expectedHelp, content) {
			t.Errorf("root help does not contain %q", content)
		}
	}

	previousIndex := -1
	for _, cmd := range testCommands {
		commandLine := "  " + cmd.name + " "
		index := strings.Index(expectedHelp, commandLine)
		if index < 0 {
			t.Errorf("root help does not list command %q", cmd.name)
		}
		if count := strings.Count(expectedHelp, commandLine); count != 1 {
			t.Errorf("root help lists command %q %d times, want once", cmd.name, count)
		}
		if index <= previousIndex {
			t.Errorf("root help command %q is out of order", cmd.name)
		}
		previousIndex = index
	}

	tests := []struct {
		name string
		args []string
	}{
		{name: "nil arguments", args: nil},
		{name: "empty arguments", args: []string{}},
		{name: "long help", args: []string{"--help"}},
		{name: "short help", args: []string{"-h"}},
		{name: "help argument", args: []string{"help"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			status, stdout, stderr := runCLI(t, test.args, "ignored")
			if status != exitSuccess {
				t.Errorf("Run() status = %d, want %d", status, exitSuccess)
			}
			if stdout != expectedHelp {
				t.Errorf("Run() stdout = %q, want canonical help %q", stdout, expectedHelp)
			}
			if stderr != "" {
				t.Errorf("Run() stderr = %q, want empty", stderr)
			}
		})
	}
}

func TestRun_CommandHelpForms(t *testing.T) {
	for _, cmd := range testCommands {
		t.Run(cmd.name, func(t *testing.T) {
			t.Parallel()

			forms := [][]string{
				{"help", cmd.name},
				{cmd.name, "--help"},
				{cmd.name, "-h"},
			}
			for _, args := range forms {
				status, stdout, stderr := runCLI(t, args, "ignored")
				if status != exitSuccess {
					t.Errorf("Run(%q) status = %d, want %d", args, status, exitSuccess)
				}
				if stdout != cmd.help {
					t.Errorf("Run(%q) stdout = %q, want command help %q", args, stdout, cmd.help)
				}
				if stderr != "" {
					t.Errorf("Run(%q) stderr = %q, want empty", args, stderr)
				}
			}

			if !cmd.isOperational && !strings.Contains(cmd.help, "not implemented in this build") {
				t.Errorf("%s help does not identify unavailable behavior", cmd.name)
			}
			if cmd.isOperational && strings.Contains(cmd.help, "not implemented in this build") {
				t.Errorf("%s help identifies operational behavior as unavailable", cmd.name)
			}
		})
	}
}

func TestRun_CommandHelpPreservesResponsibilityBoundaries(t *testing.T) {
	tests := []struct {
		name     string
		contains []string
		excludes []string
	}{
		{
			name:     "build",
			contains: []string{"one local City MMDB", "verified candidate artifact", "does not publish or replace", "separate publish command"},
			excludes: []string{"configured", "JSON", "not implemented"},
		},
		{
			name:     "compare",
			contains: []string{"coverage and disagreement", "without merging or publishing"},
		},
		{
			name:     "verify",
			contains: []string{"source or generated artifacts", "configured quality gates"},
		},
		{
			name:     "inspect",
			contains: []string{"bounded metadata", "explicitly selected lookups", "never prints or dumps", "--database", "--ip", "32", "generated stategeodb artifacts"},
		},
		{
			name:     "publish",
			contains: []string{"already built and verified candidate", "--candidate", "--destination", "reverifies", "unchanged", "atomic sibling rename", "backup or rollback", "never deleted", "macOS and Linux", "does not compile sources"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cmd, ok := findCommand(test.name)
			if !ok {
				t.Fatalf("findCommand(%q) returned nil", test.name)
			}
			for _, content := range test.contains {
				if !strings.Contains(cmd.help, content) {
					t.Errorf("%s help does not contain %q", test.name, content)
				}
			}
			for _, content := range test.excludes {
				if strings.Contains(cmd.help, content) {
					t.Errorf("%s help unexpectedly contains %q", test.name, content)
				}
			}
		})
	}
}

func TestRun_RecognizedCommandsAreUnavailable(t *testing.T) {
	for _, cmd := range testCommands {
		if cmd.isOperational {
			continue
		}
		t.Run(cmd.name, func(t *testing.T) {
			t.Parallel()

			status, stdout, stderr := runCLI(t, []string{cmd.name}, "ignored")
			if status != exitFailure {
				t.Errorf("Run() status = %d, want %d", status, exitFailure)
			}
			if stdout != "" {
				t.Errorf("Run() stdout = %q, want empty", stdout)
			}
			expectedStderr := "stategeodb: " + cmd.name + " is not implemented in this build\n"
			if stderr != expectedStderr {
				t.Errorf("Run() stderr = %q, want %q", stderr, expectedStderr)
			}
		})
	}
}

func TestRun_VersionUsesCallerValue(t *testing.T) {
	status, stdout, stderr := runCLI(t, []string{"--version"}, "test-version-123")

	if status != exitSuccess {
		t.Errorf("Run() status = %d, want %d", status, exitSuccess)
	}
	if stdout != "stategeodb test-version-123\n" {
		t.Errorf("Run() stdout = %q, want caller-provided version", stdout)
	}
	if stderr != "" {
		t.Errorf("Run() stderr = %q, want empty", stderr)
	}
}

func TestRun_RejectsInvalidUsage(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		sensitive bool
	}{
		{name: "unknown command", args: []string{"unknown"}},
		{name: "empty command", args: []string{""}},
		{name: "unsupported version alias", args: []string{"version"}},
		{name: "unsupported short version", args: []string{"-v"}},
		{name: "unsupported help spelling", args: []string{"-help"}},
		{name: "flag assignment", args: []string{"--help=true"}},
		{name: "argument separator", args: []string{"--"}},
		{name: "root help extra argument", args: []string{"--help", "extra"}},
		{name: "root short help extra argument", args: []string{"-h", "extra"}},
		{name: "version extra argument", args: []string{"--version", "extra"}},
		{name: "mixed root forms", args: []string{"--help", "--version"}},
		{name: "unknown before help", args: []string{"unknown", "--help"}},
		{name: "duplicate root help", args: []string{"--help", "--help"}},
		{name: "unknown help target", args: []string{"help", "unknown"}},
		{name: "help target extra argument", args: []string{"help", "build", "extra"}},
		{name: "command extra argument", args: []string{"compare", "extra"}},
		{name: "command help extra argument", args: []string{"verify", "--help", "extra"}},
		{name: "command short help extra argument", args: []string{"compare", "-h", "extra"}},
		{name: "unsupported nested help", args: []string{"verify", "help"}},
		{name: "token-like value", args: []string{"--token=top-secret"}, sensitive: true},
		{name: "authenticated url", args: []string{"https://user:secret@example.com/source"}, sensitive: true},
		{name: "embedded newline", args: []string{"unknown\nforged diagnostic"}, sensitive: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			status, stdout, stderr := runCLI(t, test.args, "ignored")
			if status != exitFailure {
				t.Errorf("Run() status = %d, want %d", status, exitFailure)
			}
			if stdout != "" {
				t.Errorf("Run() stdout = %q, want empty", stdout)
			}
			if stderr != unknownArgumentText {
				t.Errorf("Run() stderr = %q, want %q", stderr, unknownArgumentText)
			}
			if test.sensitive {
				for _, arg := range test.args {
					if strings.Contains(stderr, arg) {
						t.Errorf("Run() stderr echoed untrusted argument %q", arg)
					}
				}
			}
		})
	}
}

func TestRun_RepeatedInvocationsDoNotLeakState(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		version        string
		expectedStatus int
		expectedStdout string
		expectedStderr string
	}{
		{
			name:           "first version",
			args:           []string{"--version"},
			version:        "version-a",
			expectedStatus: exitSuccess,
			expectedStdout: "stategeodb version-a\n",
		},
		{
			name:           "build usage failure",
			args:           []string{"build"},
			version:        "ignored",
			expectedStatus: exitFailure,
			expectedStderr: invalidBuildUsageText,
		},
		{
			name:           "usage failure",
			args:           []string{"unknown"},
			version:        "ignored",
			expectedStatus: exitFailure,
			expectedStderr: unknownArgumentText,
		},
		{
			name:           "help after failure",
			args:           []string{"--help"},
			version:        "ignored",
			expectedStatus: exitSuccess,
			expectedStdout: helpText,
		},
		{
			name:           "command help after failure",
			args:           []string{"publish", "--help"},
			version:        "ignored",
			expectedStatus: exitSuccess,
			expectedStdout: publishHelp,
		},
		{
			name:           "second version",
			args:           []string{"--version"},
			version:        "version-b",
			expectedStatus: exitSuccess,
			expectedStdout: "stategeodb version-b\n",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			status, stdout, stderr := runCLI(t, test.args, test.version)
			if status != test.expectedStatus {
				t.Errorf("Run() status = %d, want %d", status, test.expectedStatus)
			}
			if stdout != test.expectedStdout {
				t.Errorf("Run() stdout = %q, want %q", stdout, test.expectedStdout)
			}
			if stderr != test.expectedStderr {
				t.Errorf("Run() stderr = %q, want %q", stderr, test.expectedStderr)
			}
		})
	}
}

func TestRun_OutputWriteFailure(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "root help", args: nil},
		{name: "version", args: []string{"--version"}},
		{name: "command help", args: []string{"build", "--help"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stderr bytes.Buffer
			status := Run(t.Context(), test.args, failingWriter{}, &stderr, "ignored")

			if status != exitFailure {
				t.Errorf("Run() status = %d, want %d", status, exitFailure)
			}
			if stderr.String() != outputFailureText {
				t.Errorf("Run() stderr = %q, want %q", stderr.String(), outputFailureText)
			}
		})
	}
}

func TestRun_DiagnosticWriteFailureRetainsStatus(t *testing.T) {
	tests := []struct {
		name           string
		args           []string
		expectedStatus int
	}{
		{name: "usage failure", args: []string{"unknown"}, expectedStatus: exitFailure},
		{name: "unavailable command", args: []string{"compare"}, expectedStatus: exitFailure},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			var stdout bytes.Buffer
			status := Run(t.Context(), test.args, &stdout, failingWriter{}, "ignored")
			if status != test.expectedStatus {
				t.Errorf("Run() status = %d, want %d", status, test.expectedStatus)
			}
			if stdout.String() != "" {
				t.Errorf("Run() stdout = %q, want empty", stdout.String())
			}
		})
	}
}

func runCLI(t *testing.T, args []string, version string) (int, string, string) {
	t.Helper()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	status := Run(t.Context(), args, &stdout, &stderr, version)

	return status, stdout.String(), stderr.String()
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) {
	return 0, io.ErrClosedPipe
}
