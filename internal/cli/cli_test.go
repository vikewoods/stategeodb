package cli

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestRun_HelpForms(t *testing.T) {
	expectedStatus, expectedHelp, expectedStderr := runCLI(t, nil, "ignored")
	if expectedStatus != exitSuccess {
		t.Fatalf("Run(nil) status = %d, want %d", expectedStatus, exitSuccess)
	}
	if expectedStderr != "" {
		t.Fatalf("Run(nil) stderr = %q, want empty", expectedStderr)
	}

	stableContent := []string{
		"stategeodb is the command-line foundation",
		"Usage:\n",
		"stategeodb --help",
		"stategeodb -h",
		"stategeodb help",
		"stategeodb --version",
		"supports root help and version output only",
	}
	for _, content := range stableContent {
		if !strings.Contains(expectedHelp, content) {
			t.Errorf("root help does not contain %q", content)
		}
	}

	for _, command := range []string{"build", "compare", "verify", "inspect", "publish"} {
		if strings.Contains(expectedHelp, "stategeodb "+command) {
			t.Errorf("root help advertises unimplemented command %q", command)
		}
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

func TestRun_RejectsUnknownRootArguments(t *testing.T) {
	tests := []struct {
		name      string
		arg       string
		sensitive bool
	}{
		{name: "unknown", arg: "unknown"},
		{name: "empty argument", arg: ""},
		{name: "planned command", arg: "build"},
		{name: "unsupported version alias", arg: "version"},
		{name: "unsupported short version", arg: "-v"},
		{name: "unsupported help spelling", arg: "-help"},
		{name: "flag assignment", arg: "--help=true"},
		{name: "argument separator", arg: "--"},
		{name: "token-like value", arg: "--token=top-secret", sensitive: true},
		{name: "authenticated url", arg: "https://user:secret@example.com/source", sensitive: true},
		{name: "embedded newline", arg: "unknown\nforged diagnostic", sensitive: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			status, stdout, stderr := runCLI(t, []string{test.arg}, "ignored")
			if status != exitUsage {
				t.Errorf("Run() status = %d, want %d", status, exitUsage)
			}
			if stdout != "" {
				t.Errorf("Run() stdout = %q, want empty", stdout)
			}
			if stderr != unknownArgumentText {
				t.Errorf("Run() stderr = %q, want %q", stderr, unknownArgumentText)
			}
			if test.sensitive && strings.Contains(stderr, test.arg) {
				t.Errorf("Run() stderr echoed untrusted argument %q", test.arg)
			}
		})
	}
}

func TestRun_RejectsMultipleArguments(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{name: "help with extra argument", args: []string{"--help", "extra"}},
		{name: "short help with extra argument", args: []string{"-h", "extra"}},
		{name: "help argument with extra argument", args: []string{"help", "extra"}},
		{name: "version with extra argument", args: []string{"--version", "extra"}},
		{name: "mixed supported arguments", args: []string{"--help", "--version"}},
		{name: "unknown before help", args: []string{"unknown", "--help"}},
		{name: "duplicate help", args: []string{"--help", "--help"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			status, stdout, stderr := runCLI(t, test.args, "ignored")
			if status != exitUsage {
				t.Errorf("Run() status = %d, want %d", status, exitUsage)
			}
			if stdout != "" {
				t.Errorf("Run() stdout = %q, want empty", stdout)
			}
			if stderr != unknownArgumentText {
				t.Errorf("Run() stderr = %q, want %q", stderr, unknownArgumentText)
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
			name:           "usage failure",
			args:           []string{"unknown"},
			version:        "ignored",
			expectedStatus: exitUsage,
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
	var stderr bytes.Buffer
	status := Run(t.Context(), nil, failingWriter{}, &stderr, "ignored")

	if status != exitFailure {
		t.Errorf("Run() status = %d, want %d", status, exitFailure)
	}
	if stderr.String() != outputFailureText {
		t.Errorf("Run() stderr = %q, want %q", stderr.String(), outputFailureText)
	}
}

func TestRun_UsageDiagnosticWriteFailure(t *testing.T) {
	var stdout bytes.Buffer
	status := Run(t.Context(), []string{"unknown"}, &stdout, failingWriter{}, "ignored")

	if status != exitUsage {
		t.Errorf("Run() status = %d, want %d", status, exitUsage)
	}
	if stdout.String() != "" {
		t.Errorf("Run() stdout = %q, want empty", stdout.String())
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
