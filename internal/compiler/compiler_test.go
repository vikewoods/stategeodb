package compiler

import (
	"context"
	"errors"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/vikewoods/stategeodb/internal/mmdb"
	"github.com/vikewoods/stategeodb/internal/source"
	"github.com/vikewoods/stategeodb/internal/source/maxmind"
)

const testBuildEpoch int64 = 1_700_000_000

type runtimeRecord struct {
	Country struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"country"`
	Subdivisions []struct {
		ISOCode string `maxminddb:"iso_code"`
	} `maxminddb:"subdivisions"`
}

func TestCompile(t *testing.T) {
	root := t.TempDir()
	parentFile := filepath.Join(root, "existing.txt")
	if err := os.WriteFile(parentFile, []byte("unchanged"), 0o600); err != nil {
		t.Fatalf("WriteFile(parent) error = %v", err)
	}
	sibling := filepath.Join(root, ".stategeodb-build-sibling")
	if err := os.Mkdir(sibling, 0o700); err != nil {
		t.Fatalf("Mkdir(sibling) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(sibling, "keep"), []byte("sibling"), 0o600); err != nil {
		t.Fatalf("WriteFile(sibling) error = %v", err)
	}

	request := fixtureRequest(root)
	candidate, err := Compile(t.Context(), request)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
	if candidate == nil {
		t.Fatal("Compile() candidate = nil")
	}
	candidatePath := candidate.Path()
	workspacePath := filepath.Dir(candidatePath)
	t.Cleanup(func() {
		if err := candidate.Cleanup(); err != nil {
			t.Errorf("Cleanup() error = %v", err)
		}
	})

	if filepath.Dir(workspacePath) != root {
		t.Errorf("workspace parent = %q, want request root", filepath.Dir(workspacePath))
	}
	if !strings.HasPrefix(filepath.Base(workspacePath), workspacePrefix) {
		t.Errorf("workspace name = %q, want prefix %q", filepath.Base(workspacePath), workspacePrefix)
	}
	if filepath.Base(candidatePath) != candidateName {
		t.Errorf("candidate filename = %q, want %q", filepath.Base(candidatePath), candidateName)
	}
	assertMode(t, workspacePath, os.ModeDir|0o700)
	assertMode(t, candidatePath, 0o600)

	entries, err := os.ReadDir(workspacePath)
	if err != nil {
		t.Fatalf("ReadDir(workspace) error = %v", err)
	}
	if len(entries) != 1 || entries[0].Name() != candidateName {
		t.Errorf("workspace entries = %v, want only %q", entryNames(entries), candidateName)
	}

	info, err := os.Lstat(candidatePath)
	if err != nil {
		t.Fatalf("Lstat(candidate) error = %v", err)
	}
	if !info.Mode().IsRegular() {
		t.Errorf("candidate mode = %v, want regular file", info.Mode())
	}
	if candidate.Size() <= 0 || candidate.Size() != info.Size() {
		t.Errorf("candidate size = %d, file size = %d", candidate.Size(), info.Size())
	}
	if candidate.InputRecordCount() != 250 {
		t.Errorf("InputRecordCount() = %d, want 250", candidate.InputRecordCount())
	}
	if candidate.BuildEpoch() != request.BuildEpoch {
		t.Errorf("BuildEpoch() = %d, want %d", candidate.BuildEpoch(), request.BuildEpoch)
	}
	stats := candidate.EquivalenceStats()
	if stats.SourceRecords != candidate.InputRecordCount() {
		t.Errorf(
			"EquivalenceStats().SourceRecords = %d, want InputRecordCount() %d",
			stats.SourceRecords,
			candidate.InputRecordCount(),
		)
	}
	if stats.OutputNetworks <= 0 || stats.ComparedSegments <= 0 {
		t.Errorf("EquivalenceStats() = %+v, want positive output and segment counts", stats)
	}
	if stats.ComparedSegments < stats.SourceRecords || stats.ComparedSegments < stats.OutputNetworks {
		t.Errorf("EquivalenceStats() = %+v, compared segments do not cover both streams", stats)
	}

	// A rename round trip proves Compile returned without retaining an open file
	// or verification-reader handle on platforms that forbid renaming open files.
	movedPath := candidatePath + ".moved"
	if err := os.Rename(candidatePath, movedPath); err != nil {
		t.Fatalf("Rename(candidate) error = %v", err)
	}
	if err := os.Rename(movedPath, candidatePath); err != nil {
		t.Fatalf("Rename(candidate back) error = %v", err)
	}

	database, err := maxminddb.Open(candidatePath)
	if err != nil {
		t.Fatalf("Open(candidate) error = %v", err)
	}
	if err := database.Verify(); err != nil {
		t.Fatalf("Verify(candidate) error = %v", err)
	}
	assertCandidateMetadata(t, database.Metadata, request.BuildEpoch)
	assertLookup(t, database, "2.2.3.1", true, "GB", "ENG")
	assertLookup(t, database, "2001:480:10::1", true, "US", "CA")
	assertLookup(t, database, "2.3.3.1", true, "", "")
	if err := database.Close(); err != nil {
		t.Fatalf("Close(candidate) error = %v", err)
	}

	if err := candidate.Cleanup(); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}
	if _, err := os.Lstat(candidatePath); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("Lstat(cleaned candidate) error = %v, want os.ErrNotExist", err)
	}
	if err := candidate.Cleanup(); err != nil {
		t.Errorf("second Cleanup() error = %v", err)
	}
	assertFileContents(t, parentFile, "unchanged")
	assertFileContents(t, filepath.Join(sibling, "keep"), "sibling")
}

func TestCompileAcceptsCompactedEquivalentCandidate(t *testing.T) {
	root := t.TempDir()
	records := []source.Record{
		mustRecord(t, "192.0.2.0/25", "US", "CA"),
		mustRecord(t, "192.0.2.128/25", "US", "CA"),
	}
	operations := defaultOperations()
	operations.openSource = func(string) (sourceDatabase, error) {
		return &fakeSourceDatabase{records: records}, nil
	}
	candidate, err := compile(t.Context(), fixtureRequest(root), operations)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}
	t.Cleanup(func() {
		if err := candidate.Cleanup(); err != nil {
			t.Errorf("Cleanup() error = %v", err)
		}
	})

	stats := candidate.EquivalenceStats()
	if stats.SourceRecords != 2 || stats.OutputNetworks != 1 || stats.ComparedSegments != 2 {
		t.Errorf("EquivalenceStats() = %+v, want 2 source, 1 output, 2 segments", stats)
	}
}

func TestCompileRejectsNonEquivalentCandidate(t *testing.T) {
	tests := []struct {
		name   string
		output func(*testing.T) []source.Record
	}{
		{
			name: "location mismatch",
			output: func(t *testing.T) []source.Record {
				records := testRecords(t)
				records[0].Country = "GB"
				records[0].Subdivision = "ENG"
				return records
			},
		},
		{
			name: "missing output network",
			output: func(t *testing.T) []source.Record {
				return testRecords(t)[1:]
			},
		},
		{
			name: "unexpected output network",
			output: func(t *testing.T) []source.Record {
				return append(
					testRecords(t),
					mustRecord(t, "203.0.113.0/24", "US", "NY"),
				)
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			operations := operationsWithRecords(t)
			output := test.output(t)
			operations.write = func(
				destination io.Writer,
				_ []source.Record,
				options mmdb.Options,
			) (int64, error) {
				return mmdb.Write(destination, output, options)
			}

			candidate, err := compile(t.Context(), fixtureRequest(root), operations)
			if candidate != nil {
				t.Error("compile() returned a candidate")
			}
			if !errors.Is(err, ErrNotEquivalent) {
				t.Fatalf("compile() error = %v, want errors.Is(ErrNotEquivalent)", err)
			}
			assertNoGeneratedWorkspace(t, root)
		})
	}
}

func TestCompileJoinsEquivalenceAndCleanupFailures(t *testing.T) {
	root := t.TempDir()
	var workspacePath string
	operations := operationsWithRecords(t)
	output := testRecords(t)[1:]
	operations.write = func(
		destination io.Writer,
		_ []source.Record,
		options mmdb.Options,
	) (int64, error) {
		return mmdb.Write(destination, output, options)
	}
	originalMkdirTemp := operations.mkdirTemp
	operations.mkdirTemp = func(workspaceRoot workspaceRoot, pattern string) (string, error) {
		workspace, err := originalMkdirTemp(workspaceRoot, pattern)
		workspacePath = filepath.Join(root, workspace)
		return workspace, err
	}
	operations.removeAll = func(workspaceRoot, string) error {
		return errors.New("unsafe cleanup detail")
	}

	candidate, err := compile(t.Context(), fixtureRequest(root), operations)
	if candidate != nil {
		t.Error("compile() returned a candidate")
	}
	for _, target := range []error{ErrNotEquivalent, ErrCleanup} {
		if !errors.Is(err, target) {
			t.Errorf("compile() error = %v, want errors.Is(%v)", err, target)
		}
	}
	if strings.Contains(err.Error(), "unsafe cleanup detail") {
		t.Errorf("compile() error exposed unsafe detail: %v", err)
	}
	if workspacePath == "" {
		t.Fatal("workspace was not created")
	}
	if err := os.RemoveAll(workspacePath); err != nil {
		t.Fatalf("RemoveAll(test cleanup) error = %v", err)
	}
}

func TestCompileDetectsCandidateReplacementBeforeVerification(t *testing.T) {
	root := t.TempDir()
	operations := operationsWithRecords(t)
	originalRead := operations.readFile
	operations.readFile = func(
		workspaceRoot workspaceRoot,
		name string,
		expected os.FileInfo,
	) ([]byte, error) {
		if err := workspaceRoot.Rename(name, name+".verified"); err != nil {
			t.Fatalf("Rename(candidate) error = %v", err)
		}
		replacement, err := workspaceRoot.OpenFile(
			name,
			os.O_WRONLY|os.O_CREATE|os.O_EXCL,
			0o600,
		)
		if err != nil {
			t.Fatalf("OpenFile(replacement) error = %v", err)
		}
		if _, err := replacement.Write([]byte("unverified replacement")); err != nil {
			t.Fatalf("Write(replacement) error = %v", err)
		}
		if err := replacement.Close(); err != nil {
			t.Fatalf("Close(replacement) error = %v", err)
		}
		return originalRead(workspaceRoot, name, expected)
	}

	candidate, err := compile(t.Context(), fixtureRequest(root), operations)
	if candidate != nil {
		t.Error("compile() returned a candidate")
	}
	if !errors.Is(err, ErrNotEquivalent) {
		t.Fatalf("compile() error = %v, want errors.Is(ErrNotEquivalent)", err)
	}
	if strings.Contains(err.Error(), root) || strings.Contains(err.Error(), "replacement") {
		t.Errorf("compile() error exposed unsafe detail: %v", err)
	}
	assertNoGeneratedWorkspace(t, root)
}

func TestCompileValidatesRequestBeforeOpeningSource(t *testing.T) {
	root := t.TempDir()
	regularRoot := filepath.Join(root, "not-a-directory")
	if err := os.WriteFile(regularRoot, []byte("file"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}

	tests := []struct {
		name          string
		ctx           context.Context
		request       Request
		expectedCause error
	}{
		{name: "nil context", request: fixtureRequest(root)},
		{name: "empty source path", ctx: t.Context(), request: Request{SourceID: "primary", WorkspaceRoot: root, BuildEpoch: testBuildEpoch}},
		{name: "invalid source id", ctx: t.Context(), request: Request{SourcePath: "source", SourceID: "../secret", WorkspaceRoot: root, BuildEpoch: testBuildEpoch}, expectedCause: source.ErrInvalidSourceID},
		{name: "empty workspace root", ctx: t.Context(), request: Request{SourcePath: "source", SourceID: "primary", BuildEpoch: testBuildEpoch}},
		{name: "relative workspace root", ctx: t.Context(), request: Request{SourcePath: "source", SourceID: "primary", WorkspaceRoot: ".", BuildEpoch: testBuildEpoch}},
		{name: "zero build epoch", ctx: t.Context(), request: Request{SourcePath: "source", SourceID: "primary", WorkspaceRoot: root}},
		{name: "negative build epoch", ctx: t.Context(), request: Request{SourcePath: "source", SourceID: "primary", WorkspaceRoot: root, BuildEpoch: -1}},
		{name: "missing workspace root", ctx: t.Context(), request: Request{SourcePath: "source", SourceID: "primary", WorkspaceRoot: filepath.Join(root, "super-secret-missing"), BuildEpoch: testBuildEpoch}},
		{name: "non-directory workspace root", ctx: t.Context(), request: Request{SourcePath: "source", SourceID: "primary", WorkspaceRoot: regularRoot, BuildEpoch: testBuildEpoch}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			openCalls := 0
			operations := defaultOperations()
			operations.openSource = func(string) (sourceDatabase, error) {
				openCalls++
				return nil, errors.New("unexpected source open")
			}
			before := rootEntryNames(t, root)
			candidate, err := compile(test.ctx, test.request, operations)
			if candidate != nil {
				t.Error("compile() returned a candidate")
			}
			if !errors.Is(err, ErrInvalidRequest) {
				t.Fatalf("compile() error = %v, want errors.Is(ErrInvalidRequest)", err)
			}
			if test.expectedCause != nil && !errors.Is(err, test.expectedCause) {
				t.Errorf("compile() error = %v, want errors.Is(%v)", err, test.expectedCause)
			}
			if openCalls != 0 {
				t.Errorf("source open calls = %d, want 0", openCalls)
			}
			if after := rootEntryNames(t, root); !slices.Equal(after, before) {
				t.Errorf("root entries changed: before=%v after=%v", before, after)
			}
			if strings.Contains(err.Error(), "super-secret") {
				t.Errorf("compile() error leaked workspace path: %v", err)
			}
		})
	}
}

func TestCompileContainsWorkspaceRootReplacement(t *testing.T) {
	tests := []struct {
		name      string
		configure func(*compilerOperations, func())
	}{
		{
			name: "after source closure",
			configure: func(operations *compilerOperations, replace func()) {
				operations.openSource = func(string) (sourceDatabase, error) {
					return &fakeSourceDatabase{records: testRecords(t), closeHook: replace}, nil
				}
			},
		},
		{
			name: "after workspace creation",
			configure: func(operations *compilerOperations, replace func()) {
				original := operations.mkdirTemp
				operations.mkdirTemp = func(root workspaceRoot, pattern string) (string, error) {
					name, err := original(root, pattern)
					if err == nil {
						replace()
					}
					return name, err
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "workspace")
			movedRoot := filepath.Join(parent, "workspace-moved")
			outside := t.TempDir()
			if err := os.Mkdir(root, 0o700); err != nil {
				t.Fatalf("Mkdir(root) error = %v", err)
			}

			replaced := false
			replace := func() {
				if err := os.Rename(root, movedRoot); err != nil {
					t.Fatalf("Rename(root) error = %v", err)
				}
				if err := os.Symlink(outside, root); err != nil {
					t.Fatalf("Symlink(replacement) error = %v", err)
				}
				replaced = true
			}
			operations := operationsWithRecords(t)
			test.configure(&operations, replace)
			candidate, err := compile(t.Context(), fixtureRequest(root), operations)
			if candidate != nil {
				t.Error("compile() returned a candidate")
			}
			if !errors.Is(err, ErrWorkspace) {
				t.Fatalf("compile() error = %v, want errors.Is(ErrWorkspace)", err)
			}
			if !replaced {
				t.Fatal("workspace root was not replaced")
			}
			if entries := rootEntryNames(t, outside); len(entries) != 0 {
				t.Errorf("replacement target entries = %v, want empty", entries)
			}
			assertNoGeneratedWorkspace(t, movedRoot)

			if err := os.Remove(root); err != nil {
				t.Fatalf("Remove(replacement symlink) error = %v", err)
			}
			if err := os.Rename(movedRoot, root); err != nil {
				t.Fatalf("Rename(root restore) error = %v", err)
			}
		})
	}
}

func TestCandidatePathFailsClosedAfterRootReplacement(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "workspace")
	movedRoot := filepath.Join(parent, "workspace-moved")
	outside := t.TempDir()
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatalf("Mkdir(root) error = %v", err)
	}
	candidate, err := compile(t.Context(), fixtureRequest(root), operationsWithRecords(t))
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}
	originalPath := candidate.Path()
	workspaceName := filepath.Base(filepath.Dir(originalPath))

	if err := os.Rename(root, movedRoot); err != nil {
		t.Fatalf("Rename(root) error = %v", err)
	}
	if err := os.Symlink(outside, root); err != nil {
		t.Fatalf("Symlink(replacement) error = %v", err)
	}
	attackerWorkspace := filepath.Join(outside, workspaceName)
	if err := os.Mkdir(attackerWorkspace, 0o700); err != nil {
		t.Fatalf("Mkdir(attacker workspace) error = %v", err)
	}
	attackerCandidate := filepath.Join(attackerWorkspace, candidateName)
	if err := os.WriteFile(attackerCandidate, []byte("unverified"), 0o600); err != nil {
		t.Fatalf("WriteFile(attacker candidate) error = %v", err)
	}

	if path := candidate.Path(); path != "" {
		t.Errorf("Path() after root replacement = %q, want empty", path)
	}
	if err := candidate.Cleanup(); !errors.Is(err, ErrCleanup) {
		t.Fatalf("Cleanup() after root replacement error = %v, want errors.Is(ErrCleanup)", err)
	}
	assertFileContents(t, attackerCandidate, "unverified")
	if _, err := os.Stat(filepath.Join(movedRoot, workspaceName, candidateName)); err != nil {
		t.Errorf("verified candidate Stat() error = %v", err)
	}

	if err := os.Remove(root); err != nil {
		t.Fatalf("Remove(replacement symlink) error = %v", err)
	}
	if err := os.Rename(movedRoot, root); err != nil {
		t.Fatalf("Rename(root restore) error = %v", err)
	}
	if err := candidate.Cleanup(); err != nil {
		t.Fatalf("Cleanup() after root restore error = %v", err)
	}
	if candidate.Path() != originalPath {
		t.Errorf("Path() after Cleanup = %q, want %q", candidate.Path(), originalPath)
	}
}

func TestCompileRejectsSymlinkWorkspaceRoot(t *testing.T) {
	parent := t.TempDir()
	target := t.TempDir()
	link := filepath.Join(parent, "workspace-link")
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("Symlink() unavailable: %v", err)
	}

	openCalls := 0
	operations := defaultOperations()
	operations.openSource = func(string) (sourceDatabase, error) {
		openCalls++
		return nil, errors.New("unexpected source open")
	}
	request := fixtureRequest(link)
	candidate, err := compile(t.Context(), request, operations)
	if candidate != nil {
		t.Error("compile() returned a candidate")
	}
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("compile() error = %v, want errors.Is(ErrInvalidRequest)", err)
	}
	if openCalls != 0 {
		t.Errorf("source open calls = %d, want 0", openCalls)
	}
	if entries := rootEntryNames(t, target); len(entries) != 0 {
		t.Errorf("symlink target entries = %v, want empty", entries)
	}
}

func TestCompileRejectsWorkspaceRootReplacementDuringOpen(t *testing.T) {
	root := t.TempDir()
	openCalls := 0
	operations := defaultOperations()
	operations.openRoot = func(string, os.FileInfo) (workspaceRoot, error) {
		return nil, errors.New("unsafe changed root detail")
	}
	operations.openSource = func(string) (sourceDatabase, error) {
		openCalls++
		return nil, errors.New("unexpected source open")
	}

	candidate, err := compile(t.Context(), fixtureRequest(root), operations)
	if candidate != nil {
		t.Error("compile() returned a candidate")
	}
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("compile() error = %v, want errors.Is(ErrInvalidRequest)", err)
	}
	if strings.Contains(err.Error(), "unsafe") {
		t.Errorf("compile() error exposed unsafe detail: %v", err)
	}
	if openCalls != 0 {
		t.Errorf("source open calls = %d, want 0", openCalls)
	}
}

func TestCompileSourceLifecycle(t *testing.T) {
	t.Run("open failure creates no workspace", func(t *testing.T) {
		root := t.TempDir()
		request := fixtureRequest(root)
		request.SourcePath = filepath.Join(root, "super-secret-source.mmdb")
		candidate, err := Compile(t.Context(), request)
		if candidate != nil {
			t.Error("Compile() returned a candidate")
		}
		if !errors.Is(err, maxmind.ErrOpen) {
			t.Fatalf("Compile() error = %v, want errors.Is(maxmind.ErrOpen)", err)
		}
		if strings.Contains(err.Error(), request.SourcePath) || strings.Contains(err.Error(), "super-secret") {
			t.Errorf("Compile() error leaked source path: %v", err)
		}
		assertNoGeneratedWorkspace(t, root)
	})

	t.Run("ingestion and close failures are retained", func(t *testing.T) {
		root := t.TempDir()
		operations := defaultOperations()
		operations.openSource = func(string) (sourceDatabase, error) {
			return &fakeSourceDatabase{
				recordsErr: maxmind.ErrIngest,
				closeErr:   maxmind.ErrClose,
			}, nil
		}
		workspaceCalls := 0
		operations.mkdirTemp = func(workspaceRoot, string) (string, error) {
			workspaceCalls++
			return "", errors.New("unexpected workspace creation")
		}
		candidate, err := compile(t.Context(), fixtureRequest(root), operations)
		if candidate != nil {
			t.Error("compile() returned a candidate")
		}
		for _, target := range []error{maxmind.ErrIngest, maxmind.ErrClose} {
			if !errors.Is(err, target) {
				t.Errorf("compile() error = %v, want errors.Is(%v)", err, target)
			}
		}
		if workspaceCalls != 0 {
			t.Errorf("workspace calls = %d, want 0", workspaceCalls)
		}
	})

	t.Run("source closes before workspace creation", func(t *testing.T) {
		root := t.TempDir()
		events := []string{}
		operations := defaultOperations()
		operations.openSource = func(string) (sourceDatabase, error) {
			return &fakeSourceDatabase{
				records: testRecords(t),
				events:  &events,
			}, nil
		}
		operations.mkdirTemp = func(root workspaceRoot, pattern string) (string, error) {
			events = append(events, "workspace")
			return createWorkspace(root, pattern)
		}
		candidate, err := compile(t.Context(), fixtureRequest(root), operations)
		if err != nil {
			t.Fatalf("compile() error = %v", err)
		}
		if !slices.Equal(events, []string{"records", "close", "workspace"}) {
			t.Errorf("events = %v, want records, close, workspace", events)
		}
		if err := candidate.Cleanup(); err != nil {
			t.Fatalf("Cleanup() error = %v", err)
		}
	})

	t.Run("close failure prevents workspace creation", func(t *testing.T) {
		root := t.TempDir()
		operations := defaultOperations()
		operations.openSource = func(string) (sourceDatabase, error) {
			return &fakeSourceDatabase{records: testRecords(t), closeErr: maxmind.ErrClose}, nil
		}
		workspaceCalls := 0
		operations.mkdirTemp = func(workspaceRoot, string) (string, error) {
			workspaceCalls++
			return "", nil
		}
		candidate, err := compile(t.Context(), fixtureRequest(root), operations)
		if candidate != nil {
			t.Error("compile() returned a candidate")
		}
		if !errors.Is(err, maxmind.ErrClose) {
			t.Fatalf("compile() error = %v, want errors.Is(maxmind.ErrClose)", err)
		}
		if workspaceCalls != 0 {
			t.Errorf("workspace calls = %d, want 0", workspaceCalls)
		}
	})
}

func TestCompileCancellationBoundaries(t *testing.T) {
	tests := []struct {
		name      string
		configure func(context.CancelFunc, *compilerOperations)
	}{
		{
			name: "after ingestion and closure",
			configure: func(cancel context.CancelFunc, operations *compilerOperations) {
				operations.openSource = func(string) (sourceDatabase, error) {
					return &fakeSourceDatabase{records: testRecords(t), closeHook: cancel}, nil
				}
			},
		},
		{
			name: "after workspace creation",
			configure: func(cancel context.CancelFunc, operations *compilerOperations) {
				original := operations.mkdirTemp
				operations.mkdirTemp = func(root workspaceRoot, pattern string) (string, error) {
					workspace, err := original(root, pattern)
					if err == nil {
						cancel()
					}
					return workspace, err
				}
			},
		},
		{
			name: "after writing",
			configure: func(cancel context.CancelFunc, operations *compilerOperations) {
				original := operations.write
				operations.write = func(
					destination io.Writer,
					records []source.Record,
					options mmdb.Options,
				) (int64, error) {
					written, err := original(destination, records, options)
					if err == nil {
						cancel()
					}
					return written, err
				}
			},
		},
		{
			name: "after candidate read before verification",
			configure: func(cancel context.CancelFunc, operations *compilerOperations) {
				original := operations.readFile
				operations.readFile = func(
					root workspaceRoot,
					name string,
					expected os.FileInfo,
				) ([]byte, error) {
					contents, err := original(root, name, expected)
					if err == nil {
						cancel()
					}
					return contents, err
				}
				operations.openVerification = func([]byte) (verificationDatabase, error) {
					t.Fatal("openVerification called after cancellation")
					return nil, nil
				}
			},
		},
		{
			name: "before equivalence traversal",
			configure: func(cancel context.CancelFunc, operations *compilerOperations) {
				original := operations.openVerification
				operations.openVerification = func(contents []byte) (verificationDatabase, error) {
					database, err := original(contents)
					if err != nil {
						return nil, err
					}
					return &cancelingVerificationDatabase{
						verificationDatabase: database,
						cancel:               cancel,
					}, nil
				}
			},
		},
		{
			name: "during output traversal",
			configure: func(cancel context.CancelFunc, operations *compilerOperations) {
				operations.openVerification = func([]byte) (verificationDatabase, error) {
					return &fakeVerificationDatabase{
						metadata:    expectedMetadata(testBuildEpoch),
						networks:    fakeResultsForRecords(testRecords(t)),
						networkHook: func(int) { cancel() },
					}, nil
				}
			},
		},
		{
			name: "during interval comparison",
			configure: func(_ context.CancelFunc, operations *compilerOperations) {
				original := operations.compare
				operations.compare = func(
					ctx context.Context,
					sourceRecords []source.Record,
					outputRecords []source.Record,
				) (EquivalenceStats, error) {
					cancelingContext := &cancelAfterErrChecksContext{
						Context:   ctx,
						remaining: 15,
						err:       context.Canceled,
					}
					return original(cancelingContext, sourceRecords, outputRecords)
				}
			},
		},
		{
			name: "after successful interval comparison",
			configure: func(cancel context.CancelFunc, operations *compilerOperations) {
				original := operations.compare
				operations.compare = func(
					ctx context.Context,
					sourceRecords []source.Record,
					outputRecords []source.Record,
				) (EquivalenceStats, error) {
					stats, err := original(ctx, sourceRecords, outputRecords)
					if err == nil {
						cancel()
					}
					return stats, err
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			ctx, cancel := context.WithCancel(t.Context())
			operations := operationsWithRecords(t)
			test.configure(cancel, &operations)
			candidate, err := compile(ctx, fixtureRequest(root), operations)
			if candidate != nil {
				t.Error("compile() returned a candidate")
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("compile() error = %v, want errors.Is(context.Canceled)", err)
			}
			if errors.Is(err, ErrNotEquivalent) {
				t.Errorf("compile() cancellation error = %v, unexpectedly non-equivalence", err)
			}
			assertNoGeneratedWorkspace(t, root)
		})
	}

	t.Run("pre-cancelled", func(t *testing.T) {
		root := t.TempDir()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		openCalls := 0
		operations := operationsWithRecords(t)
		operations.openSource = func(string) (sourceDatabase, error) {
			openCalls++
			return nil, errors.New("unexpected source open")
		}
		candidate, err := compile(ctx, fixtureRequest(root), operations)
		if candidate != nil {
			t.Error("compile() returned a candidate")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("compile() error = %v, want errors.Is(context.Canceled)", err)
		}
		if openCalls != 0 {
			t.Errorf("source open calls = %d, want 0", openCalls)
		}
		assertNoGeneratedWorkspace(t, root)
	})

	t.Run("expired deadline", func(t *testing.T) {
		root := t.TempDir()
		ctx, cancel := context.WithDeadline(t.Context(), time.Unix(0, 0))
		defer cancel()
		openCalls := 0
		operations := operationsWithRecords(t)
		operations.openSource = func(string) (sourceDatabase, error) {
			openCalls++
			return nil, errors.New("unexpected source open")
		}
		candidate, err := compile(ctx, fixtureRequest(root), operations)
		if candidate != nil {
			t.Error("compile() returned a candidate")
		}
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("compile() error = %v, want errors.Is(context.DeadlineExceeded)", err)
		}
		if openCalls != 0 {
			t.Errorf("source open calls = %d, want 0", openCalls)
		}
		assertNoGeneratedWorkspace(t, root)
	})
}

func TestCompileCleansWorkspaceOnFailures(t *testing.T) {
	tests := []struct {
		name          string
		expectedCause error
		configure     func(*compilerOperations)
	}{
		{
			name:          "workspace creation",
			expectedCause: ErrWorkspace,
			configure: func(operations *compilerOperations) {
				operations.mkdirTemp = func(workspaceRoot, string) (string, error) {
					return "", errors.New("unsafe workspace failure")
				}
			},
		},
		{
			name:          "candidate creation",
			expectedCause: ErrWorkspace,
			configure: func(operations *compilerOperations) {
				operations.openFile = func(workspaceRoot, string, int, os.FileMode) (candidateFile, error) {
					return nil, errors.New("unsafe create failure")
				}
			},
		},
		{
			name:          "workspace permissions",
			expectedCause: ErrWorkspace,
			configure: func(operations *compilerOperations) {
				operations.chmod = func(workspaceRoot, string, os.FileMode) error {
					return errors.New("unsafe workspace chmod failure")
				}
			},
		},
		{
			name:          "candidate permissions",
			expectedCause: ErrWorkspace,
			configure: func(operations *compilerOperations) {
				operations.openFile = faultingOpenFile(
					errors.New("unsafe file chmod failure"),
					nil,
					nil,
					nil,
				)
			},
		},
		{
			name:          "writer",
			expectedCause: mmdb.ErrWrite,
			configure: func(operations *compilerOperations) {
				operations.write = func(io.Writer, []source.Record, mmdb.Options) (int64, error) {
					return 0, mmdb.ErrWrite
				}
			},
		},
		{
			name:          "zero writer byte count",
			expectedCause: ErrWorkspace,
			configure: func(operations *compilerOperations) {
				operations.write = func(io.Writer, []source.Record, mmdb.Options) (int64, error) {
					return 0, nil
				}
			},
		},
		{
			name:          "file synchronization",
			expectedCause: ErrWorkspace,
			configure: func(operations *compilerOperations) {
				operations.openFile = faultingOpenFile(nil, errors.New("unsafe sync failure"), nil, nil)
			},
		},
		{
			name:          "open candidate stat",
			expectedCause: ErrWorkspace,
			configure: func(operations *compilerOperations) {
				operations.openFile = faultingOpenFile(
					nil,
					nil,
					nil,
					errors.New("unsafe open file stat failure"),
				)
			},
		},
		{
			name:          "file close",
			expectedCause: ErrWorkspace,
			configure: func(operations *compilerOperations) {
				operations.openFile = faultingOpenFile(nil, nil, errors.New("unsafe close failure"), nil)
			},
		},
		{
			name:          "verification open",
			expectedCause: ErrVerify,
			configure: func(operations *compilerOperations) {
				operations.openVerification = func([]byte) (verificationDatabase, error) {
					return nil, errors.New("unsafe verification path")
				}
			},
		},
		{
			name:          "candidate verification read",
			expectedCause: ErrVerify,
			configure: func(operations *compilerOperations) {
				operations.readFile = func(workspaceRoot, string, os.FileInfo) ([]byte, error) {
					return nil, errors.New("unsafe candidate read detail")
				}
			},
		},
		{
			name:          "structural verification",
			expectedCause: ErrVerify,
			configure: func(operations *compilerOperations) {
				operations.openVerification = func([]byte) (verificationDatabase, error) {
					return &fakeVerificationDatabase{verifyErr: errors.New("unsafe structure detail")}, nil
				}
			},
		},
		{
			name:          "metadata mismatch",
			expectedCause: ErrVerify,
			configure: func(operations *compilerOperations) {
				metadata := expectedMetadata(testBuildEpoch)
				metadata.DatabaseType = "wrong"
				operations.openVerification = func([]byte) (verificationDatabase, error) {
					return &fakeVerificationDatabase{metadata: metadata}, nil
				}
			},
		},
		{
			name:          "verification close",
			expectedCause: ErrVerify,
			configure: func(operations *compilerOperations) {
				operations.openVerification = func([]byte) (verificationDatabase, error) {
					return &fakeVerificationDatabase{
						metadata: expectedMetadata(testBuildEpoch),
						networks: fakeResultsForRecords(testRecords(t)),
						closeErr: errors.New("unsafe verifier close detail"),
					}, nil
				}
			},
		},
		{
			name:          "candidate size mismatch",
			expectedCause: ErrWorkspace,
			configure: func(operations *compilerOperations) {
				original := operations.write
				operations.write = func(
					destination io.Writer,
					records []source.Record,
					options mmdb.Options,
				) (int64, error) {
					written, err := original(destination, records, options)
					return written + 1, err
				}
			},
		},
		{
			name:          "candidate stat",
			expectedCause: ErrWorkspace,
			configure: func(operations *compilerOperations) {
				original := operations.rootLstat
				operations.rootLstat = func(root workspaceRoot, path string) (os.FileInfo, error) {
					if filepath.Base(path) == candidateName {
						return nil, errors.New("unsafe stat failure")
					}
					return original(root, path)
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			parentFile := filepath.Join(root, "keep")
			if err := os.WriteFile(parentFile, []byte("keep"), 0o600); err != nil {
				t.Fatalf("WriteFile() error = %v", err)
			}
			sibling := filepath.Join(root, workspacePrefix+"sibling")
			if err := os.Mkdir(sibling, 0o700); err != nil {
				t.Fatalf("Mkdir(sibling) error = %v", err)
			}
			if err := os.WriteFile(filepath.Join(sibling, "keep"), []byte("sibling"), 0o600); err != nil {
				t.Fatalf("WriteFile(sibling) error = %v", err)
			}
			operations := operationsWithRecords(t)
			test.configure(&operations)
			candidate, err := compile(t.Context(), fixtureRequest(root), operations)
			if candidate != nil {
				t.Error("compile() returned a candidate")
			}
			if !errors.Is(err, test.expectedCause) {
				t.Fatalf("compile() error = %v, want errors.Is(%v)", err, test.expectedCause)
			}
			assertNoGeneratedWorkspace(t, root, filepath.Base(sibling))
			assertFileContents(t, parentFile, "keep")
			assertFileContents(t, filepath.Join(sibling, "keep"), "sibling")
			if strings.Contains(err.Error(), "unsafe") {
				t.Errorf("compile() error exposed unsafe detail: %v", err)
			}
		})
	}
}

func TestCompileJoinsPrimaryAndCleanupFailures(t *testing.T) {
	root := t.TempDir()
	var workspacePath string
	operations := operationsWithRecords(t)
	originalMkdirTemp := operations.mkdirTemp
	operations.mkdirTemp = func(workspaceRoot workspaceRoot, pattern string) (string, error) {
		workspace, err := originalMkdirTemp(workspaceRoot, pattern)
		workspacePath = filepath.Join(root, workspace)
		return workspace, err
	}
	operations.write = func(io.Writer, []source.Record, mmdb.Options) (int64, error) {
		return 0, mmdb.ErrWrite
	}
	operations.removeAll = func(workspaceRoot, string) error {
		return errors.New("unsafe cleanup detail")
	}

	candidate, err := compile(t.Context(), fixtureRequest(root), operations)
	if candidate != nil {
		t.Error("compile() returned a candidate")
	}
	for _, target := range []error{mmdb.ErrWrite, ErrCleanup} {
		if !errors.Is(err, target) {
			t.Errorf("compile() error = %v, want errors.Is(%v)", err, target)
		}
	}
	if strings.Contains(err.Error(), "unsafe cleanup detail") {
		t.Errorf("compile() error exposed cleanup detail: %v", err)
	}
	if workspacePath == "" {
		t.Fatal("workspace was not created")
	}
	if _, statErr := os.Stat(workspacePath); statErr != nil {
		t.Errorf("failed cleanup workspace Stat() error = %v", statErr)
	}
	if removeErr := os.RemoveAll(workspacePath); removeErr != nil {
		t.Fatalf("RemoveAll(test cleanup) error = %v", removeErr)
	}
}

func TestCandidateCleanupFailureIsRetryable(t *testing.T) {
	root := t.TempDir()
	isFailing := true
	operations := operationsWithRecords(t)
	operations.removeAll = func(root workspaceRoot, path string) error {
		if isFailing {
			return errors.New("unsafe cleanup detail")
		}
		return root.RemoveAll(path)
	}
	candidate, err := compile(t.Context(), fixtureRequest(root), operations)
	if err != nil {
		t.Fatalf("compile() error = %v", err)
	}
	path := candidate.Path()
	if err := candidate.Cleanup(); !errors.Is(err, ErrCleanup) {
		t.Fatalf("Cleanup() error = %v, want errors.Is(ErrCleanup)", err)
	} else if strings.Contains(err.Error(), "unsafe cleanup detail") {
		t.Errorf("Cleanup() error exposed unsafe detail: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("candidate after failed cleanup Stat() error = %v", err)
	}

	isFailing = false
	if err := candidate.Cleanup(); err != nil {
		t.Fatalf("retry Cleanup() error = %v", err)
	}
	if err := candidate.Cleanup(); err != nil {
		t.Errorf("repeated Cleanup() error = %v", err)
	}
}

func TestCandidateCleanupPreservesSiblingCandidate(t *testing.T) {
	root := t.TempDir()
	first, err := compile(t.Context(), fixtureRequest(root), operationsWithRecords(t))
	if err != nil {
		t.Fatalf("first compile() error = %v", err)
	}
	second, err := compile(t.Context(), fixtureRequest(root), operationsWithRecords(t))
	if err != nil {
		_ = first.Cleanup()
		t.Fatalf("second compile() error = %v", err)
	}
	t.Cleanup(func() {
		if err := first.Cleanup(); err != nil {
			t.Errorf("first Cleanup() error = %v", err)
		}
		if err := second.Cleanup(); err != nil {
			t.Errorf("second Cleanup() error = %v", err)
		}
	})

	if filepath.Dir(first.Path()) == filepath.Dir(second.Path()) {
		t.Fatalf("candidate workspaces are not unique: %q", filepath.Dir(first.Path()))
	}
	if err := first.Cleanup(); err != nil {
		t.Fatalf("first Cleanup() error = %v", err)
	}
	if _, err := os.Stat(second.Path()); err != nil {
		t.Errorf("sibling candidate Stat() error after first cleanup = %v", err)
	}
	if err := second.Cleanup(); err != nil {
		t.Fatalf("second Cleanup() error = %v", err)
	}
}

func TestCandidateNilReceiver(t *testing.T) {
	var candidate *Candidate
	if candidate.Path() != "" || candidate.InputRecordCount() != 0 ||
		candidate.Size() != 0 || candidate.BuildEpoch() != 0 ||
		candidate.EquivalenceStats() != (EquivalenceStats{}) {
		t.Error("nil Candidate accessors did not return zero values")
	}
	if err := candidate.Cleanup(); err != nil {
		t.Errorf("nil Cleanup() error = %v", err)
	}
}

func fakeResultsForRecords(records []source.Record) []networkResult {
	results := make([]networkResult, 0, len(records))
	for _, record := range records {
		result := &fakeNetworkResult{prefix: record.Prefix}
		if record.Country != "" {
			country := record.Country
			result.country = &country
		}
		if record.Subdivision != "" {
			subdivision := record.Subdivision
			result.subdivision = &subdivision
		}
		results = append(results, result)
	}
	return results
}

func operationsWithRecords(t *testing.T) compilerOperations {
	t.Helper()
	operations := defaultOperations()
	operations.openSource = func(string) (sourceDatabase, error) {
		return &fakeSourceDatabase{records: testRecords(t)}, nil
	}
	return operations
}

func fixtureRequest(root string) Request {
	return Request{
		SourcePath:    filepath.Join("..", "..", "testdata", "maxmind", "GeoIP2-City-Test.mmdb"),
		SourceID:      "primary",
		WorkspaceRoot: root,
		BuildEpoch:    testBuildEpoch,
	}
}

func testRecords(t *testing.T) []source.Record {
	t.Helper()
	return []source.Record{
		mustRecord(t, "192.0.2.0/24", "US", "CA"),
		mustRecord(t, "198.51.100.0/24", "", ""),
		mustRecord(t, "2001:db8::/32", "GB", ""),
	}
}

func mustRecord(t *testing.T, prefix, country, subdivision string) source.Record {
	t.Helper()
	record, err := source.NewRecord(
		netip.MustParsePrefix(prefix),
		country,
		subdivision,
		"primary",
	)
	if err != nil {
		t.Fatalf("NewRecord() error = %v", err)
	}
	return record
}

func expectedMetadata(buildEpoch int64) maxminddb.Metadata {
	return maxminddb.Metadata{
		Description:              map[string]string{"en": mmdb.SchemaDescription},
		DatabaseType:             mmdb.DatabaseType,
		Languages:                []string{},
		BinaryFormatMajorVersion: 2,
		BinaryFormatMinorVersion: 0,
		BuildEpoch:               uint(buildEpoch),
		IPVersion:                6,
		NodeCount:                1,
		RecordSize:               mmdb.RecordSize,
	}
}

func TestMetadataMatchesRequiresCurrentCompilerEncoding(t *testing.T) {
	metadata := expectedMetadata(testBuildEpoch)
	if !metadataMatches(metadata, testBuildEpoch) {
		t.Fatal("metadataMatches(current) = false")
	}
	metadata.RecordSize = mmdb.LegacyRecordSize
	if metadataMatches(metadata, testBuildEpoch) {
		t.Fatal("metadataMatches(legacy) = true")
	}
}

func assertCandidateMetadata(t *testing.T, metadata maxminddb.Metadata, buildEpoch int64) {
	t.Helper()
	if !metadataMatches(metadata, buildEpoch) {
		t.Errorf("candidate metadata = %+v, want fixed compiler metadata", metadata)
	}
}

func assertLookup(
	t *testing.T,
	database *maxminddb.Reader,
	address string,
	expectedFound bool,
	expectedCountry string,
	expectedSubdivision string,
) {
	t.Helper()
	result := database.Lookup(netip.MustParseAddr(address))
	if err := result.Err(); err != nil {
		t.Fatalf("Lookup(%q) error = %v", address, err)
	}
	if result.Found() != expectedFound {
		t.Fatalf("Lookup(%q) Found() = %t, want %t", address, result.Found(), expectedFound)
	}
	var record runtimeRecord
	if err := result.Decode(&record); err != nil {
		t.Fatalf("Lookup(%q) Decode() error = %v", address, err)
	}
	actualSubdivision := ""
	if len(record.Subdivisions) > 0 {
		actualSubdivision = record.Subdivisions[0].ISOCode
	}
	if record.Country.ISOCode != expectedCountry || actualSubdivision != expectedSubdivision {
		t.Errorf(
			"Lookup(%q) location = %q/%q, want %q/%q",
			address,
			record.Country.ISOCode,
			actualSubdivision,
			expectedCountry,
			expectedSubdivision,
		)
	}
}

func assertMode(t *testing.T, path string, expected os.FileMode) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatalf("Lstat() error = %v", err)
	}
	actual := info.Mode() & (os.ModeType | os.ModePerm)
	if actual != expected {
		t.Errorf("mode = %v, want %v", actual, expected)
	}
}

func assertFileContents(t *testing.T, path, expected string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if string(data) != expected {
		t.Errorf("file contents = %q, want %q", data, expected)
	}
}

func assertNoGeneratedWorkspace(t *testing.T, root string, allowed ...string) {
	t.Helper()
	allowedNames := make(map[string]struct{}, len(allowed))
	for _, name := range allowed {
		allowedNames[name] = struct{}{}
	}
	for _, name := range rootEntryNames(t, root) {
		_, isAllowed := allowedNames[name]
		if strings.HasPrefix(name, workspacePrefix) && !isAllowed {
			t.Errorf("generated workspace remains: %q", name)
		}
	}
}

func rootEntryNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("ReadDir(root) error = %v", err)
	}
	return entryNames(entries)
}

func entryNames(entries []os.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return names
}

type fakeSourceDatabase struct {
	records    []source.Record
	recordsErr error
	closeErr   error
	closeHook  func()
	events     *[]string
}

func (database *fakeSourceDatabase) Records(context.Context, string) ([]source.Record, error) {
	if database.events != nil {
		*database.events = append(*database.events, "records")
	}
	return slices.Clone(database.records), database.recordsErr
}

func (database *fakeSourceDatabase) Close() error {
	if database.events != nil {
		*database.events = append(*database.events, "close")
	}
	if database.closeHook != nil {
		database.closeHook()
	}
	return database.closeErr
}

type faultFile struct {
	*os.File
	chmodErr error
	syncErr  error
	closeErr error
	statErr  error
}

func (file *faultFile) Chmod(mode os.FileMode) error {
	if file.chmodErr != nil {
		return file.chmodErr
	}
	return file.File.Chmod(mode)
}

func (file *faultFile) Sync() error {
	if file.syncErr != nil {
		return file.syncErr
	}
	return file.File.Sync()
}

func (file *faultFile) Stat() (os.FileInfo, error) {
	if file.statErr != nil {
		return nil, file.statErr
	}
	return file.File.Stat()
}

func (file *faultFile) Close() error {
	return errors.Join(file.File.Close(), file.closeErr)
}

func faultingOpenFile(
	chmodErr error,
	syncErr error,
	closeErr error,
	statErr error,
) func(workspaceRoot, string, int, os.FileMode) (candidateFile, error) {
	return func(root workspaceRoot, path string, flags int, mode os.FileMode) (candidateFile, error) {
		file, err := root.OpenFile(path, flags, mode)
		if err != nil {
			return nil, err
		}
		return &faultFile{
			File:     file,
			chmodErr: chmodErr,
			syncErr:  syncErr,
			closeErr: closeErr,
			statErr:  statErr,
		}, nil
	}
}

type fakeVerificationDatabase struct {
	metadata    maxminddb.Metadata
	verifyErr   error
	closeErr    error
	networks    []networkResult
	networkHook func(int)
	closeCalls  *int
}

func (database *fakeVerificationDatabase) Verify() error {
	return database.verifyErr
}

func (database *fakeVerificationDatabase) Metadata() maxminddb.Metadata {
	return database.metadata
}

func (database *fakeVerificationDatabase) Networks() networkIterator {
	return func(yield func(networkResult) bool) {
		for index, result := range database.networks {
			if !yield(result) {
				return
			}
			if database.networkHook != nil {
				database.networkHook(index)
			}
		}
	}
}

func (database *fakeVerificationDatabase) Close() error {
	if database.closeCalls != nil {
		(*database.closeCalls)++
	}
	return database.closeErr
}

type cancelingVerificationDatabase struct {
	verificationDatabase
	cancel context.CancelFunc
}

func (database *cancelingVerificationDatabase) Verify() error {
	err := database.verificationDatabase.Verify()
	if err == nil {
		database.cancel()
	}
	return err
}

func (database *cancelingVerificationDatabase) Metadata() maxminddb.Metadata {
	return database.verificationDatabase.Metadata()
}

func (database *cancelingVerificationDatabase) Networks() networkIterator {
	return database.verificationDatabase.Networks()
}

func (database *cancelingVerificationDatabase) Close() error {
	return database.verificationDatabase.Close()
}

var _ sourceDatabase = (*fakeSourceDatabase)(nil)
var _ candidateFile = (*faultFile)(nil)
var _ verificationDatabase = (*fakeVerificationDatabase)(nil)
var _ verificationDatabase = (*cancelingVerificationDatabase)(nil)
