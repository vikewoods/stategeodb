package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"

	"github.com/vikewoods/stategeodb/internal/artifact"
	"github.com/vikewoods/stategeodb/internal/inspect"
	"github.com/vikewoods/stategeodb/internal/mmdb"
	"github.com/vikewoods/stategeodb/internal/source"
)

const testBuildEpoch int64 = 1_700_000_321

func TestPublish_CreatesVerifiedDestination(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
	destination := filepath.Join(destinationDirectory, "stategeo.mmdb")
	candidateBefore := readFile(t, candidate)

	renameCalls := 0
	operations := operationsForDestination(t, destinationDirectory, func(opened root) root {
		return &hookRoot{root: opened, rename: func(oldName, newName string) error {
			renameCalls++
			return opened.Rename(oldName, newName)
		}}
	})
	result, err := publish(t.Context(), Request{
		CandidatePath:   candidate,
		DestinationPath: destination,
	}, operations)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	expectedDigest := sha256.Sum256(candidateBefore)
	if result.Action != ActionCreated || result.Size != int64(len(candidateBefore)) ||
		result.SHA256 != expectedDigest || renameCalls != 1 {
		t.Errorf("Publish() result = %+v, want created/%d/%x", result, len(candidateBefore), expectedDigest)
	}
	if actual := readFile(t, destination); !bytes.Equal(actual, candidateBefore) {
		t.Error("destination bytes differ from candidate")
	}
	info, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("Stat(destination) error = %v", err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("destination mode = %o, want 644", info.Mode().Perm())
	}
	if _, err := inspect.Inspect(t.Context(), inspect.Request{DatabasePath: destination}); err != nil {
		t.Errorf("Inspect(destination) error = %v", err)
	}
	assertCandidateUnchanged(t, candidate, candidateBefore)
	assertNoPublicationArtifacts(t, destinationDirectory, "stategeo.mmdb")
}

func TestPublish_CreatesDestinationFromLegacyCandidate(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCompatibleCandidate(
		t,
		candidateDirectory,
		"candidate.mmdb",
		testBuildEpoch,
		"US",
		mmdb.LegacyRecordSize,
	)
	destination := filepath.Join(destinationDirectory, "stategeo.mmdb")
	candidateBefore := readFile(t, candidate)

	result, err := Publish(t.Context(), Request{
		CandidatePath:   candidate,
		DestinationPath: destination,
	})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result.Action != ActionCreated {
		t.Errorf("Publish() action = %q, want %q", result.Action, ActionCreated)
	}
	if !bytes.Equal(readFile(t, destination), candidateBefore) {
		t.Error("destination bytes differ from legacy candidate")
	}
	inspection, err := inspect.Inspect(
		t.Context(),
		inspect.Request{DatabasePath: destination},
	)
	if err != nil {
		t.Fatalf("Inspect(destination) error = %v", err)
	}
	if inspection.Metadata.RecordSize != mmdb.LegacyRecordSize {
		t.Errorf(
			"destination record size = %d, want %d",
			inspection.Metadata.RecordSize,
			mmdb.LegacyRecordSize,
		)
	}
	assertCandidateUnchanged(t, candidate, candidateBefore)
	assertNoPublicationArtifacts(t, destinationDirectory, "stategeo.mmdb")
}

func TestPublish_IdenticalDestinationIsUnchanged(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
	candidateBefore := readFile(t, candidate)
	destination := filepath.Join(destinationDirectory, "stategeo.mmdb")
	if err := os.WriteFile(destination, candidateBefore, 0o600); err != nil {
		t.Fatalf("WriteFile(destination) error = %v", err)
	}
	fixedTime := time.Unix(1_650_000_000, 0)
	if err := os.Chtimes(destination, fixedTime, fixedTime); err != nil {
		t.Fatalf("Chtimes(destination) error = %v", err)
	}
	before, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("Stat(destination) error = %v", err)
	}

	renameCalls := 0
	operations := operationsForDestination(t, destinationDirectory, func(opened root) root {
		return &hookRoot{root: opened, rename: func(oldName, newName string) error {
			renameCalls++
			return opened.Rename(oldName, newName)
		}}
	})
	result, err := publish(t.Context(), Request{
		CandidatePath:   candidate,
		DestinationPath: destination,
	}, operations)
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	after, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("Stat(destination after) error = %v", err)
	}
	if result.Action != ActionUnchanged || !os.SameFile(before, after) || renameCalls != 0 {
		t.Errorf("Publish() result/identity = %+v/%t, want unchanged/same", result, os.SameFile(before, after))
	}
	if !after.ModTime().Equal(before.ModTime()) || after.Mode() != before.Mode() {
		t.Errorf("destination metadata changed: before=%v/%v after=%v/%v", before.ModTime(), before.Mode(), after.ModTime(), after.Mode())
	}
	assertCandidateUnchanged(t, candidate, candidateBefore)
	assertNoPublicationArtifacts(t, destinationDirectory, "stategeo.mmdb")
}

func TestPublish_ReplacesLegacyEncodingThenBecomesUnchanged(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(
		t,
		candidateDirectory,
		"candidate.mmdb",
		testBuildEpoch,
		"US",
	)
	destination := writeCompatibleCandidate(
		t,
		destinationDirectory,
		"stategeo.mmdb",
		testBuildEpoch,
		"US",
		mmdb.LegacyRecordSize,
	)
	candidateBefore := readFile(t, candidate)
	if bytes.Equal(candidateBefore, readFile(t, destination)) {
		t.Fatal("current and legacy encodings produced identical bytes")
	}

	result, err := Publish(t.Context(), Request{
		CandidatePath:   candidate,
		DestinationPath: destination,
	})
	if err != nil {
		t.Fatalf("first Publish() error = %v", err)
	}
	if result.Action != ActionReplaced {
		t.Errorf("first Publish() action = %q, want %q", result.Action, ActionReplaced)
	}
	if !bytes.Equal(readFile(t, destination), candidateBefore) {
		t.Error("replacement bytes differ from current candidate")
	}
	inspection, err := inspect.Inspect(
		t.Context(),
		inspect.Request{DatabasePath: destination},
	)
	if err != nil {
		t.Fatalf("Inspect(replacement) error = %v", err)
	}
	if inspection.Metadata.RecordSize != mmdb.RecordSize {
		t.Errorf(
			"replacement record size = %d, want %d",
			inspection.Metadata.RecordSize,
			mmdb.RecordSize,
		)
	}

	result, err = Publish(t.Context(), Request{
		CandidatePath:   candidate,
		DestinationPath: destination,
	})
	if err != nil {
		t.Fatalf("second Publish() error = %v", err)
	}
	if result.Action != ActionUnchanged {
		t.Errorf("second Publish() action = %q, want %q", result.Action, ActionUnchanged)
	}
	assertCandidateUnchanged(t, candidate, candidateBefore)
	assertNoPublicationArtifacts(t, destinationDirectory, "stategeo.mmdb")
}

func TestPublish_CandidateMayAlreadyBeDestination(t *testing.T) {
	directory := t.TempDir()
	path := writeCandidate(t, directory, "stategeo.mmdb", testBuildEpoch, "US")
	before, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat() error = %v", err)
	}
	result, err := Publish(t.Context(), Request{CandidatePath: path, DestinationPath: path})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat(after) error = %v", err)
	}
	if result.Action != ActionUnchanged || !os.SameFile(before, after) {
		t.Errorf("Publish() = %+v, identity same = %t", result, os.SameFile(before, after))
	}
	assertNoPublicationArtifacts(t, directory, "stategeo.mmdb")
}

func TestPublish_ReplacesDifferentDestinationAtomically(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch+1, "GB")
	destination := writeCandidate(t, destinationDirectory, "stategeo.mmdb", testBuildEpoch, "US")
	candidateBefore := readFile(t, candidate)
	destinationBefore := readFile(t, destination)
	destinationInfoBefore, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("Stat(destination) error = %v", err)
	}

	operations := defaultOperations()
	baseOpenRoot := operations.openRoot
	renameCalls := 0
	operations.openRoot = func(path string) (root, error) {
		opened, err := baseOpenRoot(path)
		if err != nil || path != destinationDirectory+string(os.PathSeparator) {
			return opened, err
		}
		return &hookRoot{
			root: opened,
			rename: func(oldName string, newName string) error {
				renameCalls++
				if actual := readFile(t, destination); !bytes.Equal(actual, destinationBefore) {
					t.Error("destination changed before rename commit point")
				}
				return opened.Rename(oldName, newName)
			},
		}, nil
	}
	result, err := publish(t.Context(), Request{
		CandidatePath:   candidate,
		DestinationPath: destination,
	}, operations)
	if err != nil {
		t.Fatalf("publish() error = %v", err)
	}
	if result.Action != ActionReplaced || renameCalls != 1 {
		t.Errorf("publish() result/renames = %+v/%d, want replaced/1", result, renameCalls)
	}
	if actual := readFile(t, destination); !bytes.Equal(actual, candidateBefore) {
		t.Error("replacement bytes differ from candidate")
	}
	destinationInfoAfter, err := os.Stat(destination)
	if err != nil {
		t.Fatalf("Stat(destination after) error = %v", err)
	}
	if os.SameFile(destinationInfoBefore, destinationInfoAfter) {
		t.Error("replacement retained old destination identity")
	}
	if _, err := inspect.Inspect(t.Context(), inspect.Request{DatabasePath: destination}); err != nil {
		t.Errorf("Inspect(replacement) error = %v", err)
	}
	assertCandidateUnchanged(t, candidate, candidateBefore)
	assertNoPublicationArtifacts(t, destinationDirectory, "stategeo.mmdb")
}

func TestPublish_ReplacesEqualSizeDifferentBytes(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
	destination := filepath.Join(destinationDirectory, "stategeo.mmdb")
	candidateBytes := readFile(t, candidate)
	different := slices.Clone(candidateBytes)
	different[0] ^= 0xff
	if err := os.WriteFile(destination, different, 0o644); err != nil {
		t.Fatalf("WriteFile(destination) error = %v", err)
	}

	result, err := Publish(t.Context(), Request{CandidatePath: candidate, DestinationPath: destination})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result.Action != ActionReplaced || !bytes.Equal(readFile(t, destination), candidateBytes) {
		t.Errorf("Publish() result = %+v, destination was not replaced", result)
	}
}

func TestPublish_ReplacesDifferentSize(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
	destination := writeBytes(t, destinationDirectory, "stategeo.mmdb", []byte("short destination"))
	candidateBytes := readFile(t, candidate)
	result, err := Publish(t.Context(), Request{CandidatePath: candidate, DestinationPath: destination})
	if err != nil {
		t.Fatalf("Publish() error = %v", err)
	}
	if result.Action != ActionReplaced || !bytes.Equal(readFile(t, destination), candidateBytes) {
		t.Errorf("Publish() result = %+v, destination was not replaced", result)
	}
}

func TestPublish_ExactComparisonDoesNotTrustDigestAlone(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
	destination := writeCandidate(t, destinationDirectory, "stategeo.mmdb", testBuildEpoch+1, "GB")
	candidateBytes := readFile(t, candidate)
	candidateDigest := sha256.Sum256(candidateBytes)
	operations := defaultOperations()
	operations.compare = func(context.Context, io.Reader, io.Reader) (comparison, error) {
		return comparison{isEqual: false, destinationSHA256: candidateDigest}, nil
	}
	result, err := publish(t.Context(), Request{CandidatePath: candidate, DestinationPath: destination}, operations)
	if err != nil {
		t.Fatalf("publish() error = %v", err)
	}
	if result.Action != ActionReplaced || !bytes.Equal(readFile(t, destination), candidateBytes) {
		t.Errorf("publish() result = %+v, equal injected digest incorrectly prevented replacement", result)
	}
}

func TestCompareStreams_EqualContentIgnoresReadChunkBoundaries(t *testing.T) {
	contents := bytes.Repeat([]byte("stategeodb"), streamBufferSize/2+17)
	result, err := compareStreams(
		t.Context(),
		&maximumChunkReader{reader: bytes.NewReader(contents), maximum: 1},
		&maximumChunkReader{reader: bytes.NewReader(contents), maximum: 37},
	)
	if err != nil {
		t.Fatalf("compareStreams() error = %v", err)
	}
	if !result.isEqual || result.destinationSHA256 != sha256.Sum256(contents) {
		t.Errorf("compareStreams() = %+v, want equal/%x", result, sha256.Sum256(contents))
	}
}

func TestCompareStreams_CancellationDuringShortReads(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	contents := bytes.Repeat([]byte("x"), streamBufferSize)
	candidate := &cancelAfterReadReader{
		reader: &maximumChunkReader{reader: bytes.NewReader(contents), maximum: 1},
		cancel: cancel,
	}
	_, err := compareStreams(ctx, candidate, bytes.NewReader(contents))
	if !errors.Is(err, context.Canceled) {
		t.Errorf("compareStreams() error = %v, want context.Canceled", err)
	}
}

func TestCompareStreams_RejectsZeroProgressReader(t *testing.T) {
	_, err := compareStreams(t.Context(), zeroProgressReader{}, bytes.NewReader([]byte("content")))
	if !errors.Is(err, ErrCompare) {
		t.Errorf("compareStreams() error = %v, want ErrCompare", err)
	}
}

func TestPublish_DestinationIdentityChangePreventsUnchanged(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
	candidateBytes := readFile(t, candidate)
	destination := writeBytes(t, destinationDirectory, "stategeo.mmdb", candidateBytes)
	displaced := filepath.Join(destinationDirectory, "externally-displaced.mmdb")
	operations := defaultOperations()
	baseCompare := operations.compare
	operations.compare = func(ctx context.Context, left io.Reader, right io.Reader) (comparison, error) {
		if err := os.Rename(destination, displaced); err != nil {
			t.Fatalf("Rename(destination) error = %v", err)
		}
		if err := os.WriteFile(destination, candidateBytes, 0o644); err != nil {
			t.Fatalf("WriteFile(replacement) error = %v", err)
		}
		return baseCompare(ctx, left, right)
	}
	result, err := publish(t.Context(), Request{CandidatePath: candidate, DestinationPath: destination}, operations)
	if !errors.Is(err, ErrCompare) || result != (Result{}) {
		t.Errorf("publish() = %+v/%v, want empty/ErrCompare", result, err)
	}
	if !bytes.Equal(readFile(t, destination), candidateBytes) {
		t.Error("publisher modified externally replaced destination")
	}
	assertNoTemporaryArtifacts(t, destinationDirectory)
}

func TestPublish_CandidateIdentityChangeFailsBeforeCopy(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
	destination := writeCandidate(t, destinationDirectory, "stategeo.mmdb", testBuildEpoch+1, "GB")
	destinationBefore := readFile(t, destination)
	operations := defaultOperations()
	baseOpenRoot := operations.openRoot
	operations.openRoot = func(path string) (root, error) {
		opened, err := baseOpenRoot(path)
		if err != nil || path != candidateDirectory+string(os.PathSeparator) {
			return opened, err
		}
		return &hookRoot{root: opened, open: func(name string) (file, error) {
			bound, openErr := opened.Open(name)
			if openErr != nil {
				return nil, openErr
			}
			if renameErr := os.Rename(candidate, candidate+".old"); renameErr != nil {
				t.Fatalf("Rename(candidate) error = %v", renameErr)
			}
			if writeErr := os.WriteFile(candidate, []byte("external replacement"), 0o600); writeErr != nil {
				t.Fatalf("WriteFile(candidate replacement) error = %v", writeErr)
			}
			return bound, nil
		}}, nil
	}
	_, err := publish(t.Context(), Request{CandidatePath: candidate, DestinationPath: destination}, operations)
	if !errors.Is(err, ErrCandidate) {
		t.Errorf("publish() error = %v, want ErrCandidate", err)
	}
	if !bytes.Equal(readFile(t, destination), destinationBefore) {
		t.Error("candidate identity change altered destination")
	}
	assertNoTemporaryArtifacts(t, destinationDirectory)
}

func TestPublish_DestinationParentIdentityChangeFailsBeforeCommit(t *testing.T) {
	candidateDirectory := t.TempDir()
	containerDirectory := t.TempDir()
	destinationDirectory := filepath.Join(containerDirectory, "published")
	if err := os.Mkdir(destinationDirectory, 0o755); err != nil {
		t.Fatalf("Mkdir(destination) error = %v", err)
	}
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
	destination := writeCandidate(t, destinationDirectory, "stategeo.mmdb", testBuildEpoch+1, "GB")
	destinationBefore := readFile(t, destination)
	displacedDirectory := filepath.Join(containerDirectory, "displaced")
	operations := defaultOperations()
	baseStatPath := operations.statPath
	destinationStats := 0
	operations.statPath = func(path string) (os.FileInfo, error) {
		if path == destinationDirectory+string(os.PathSeparator) {
			destinationStats++
			if destinationStats == 2 {
				if err := os.Rename(destinationDirectory, displacedDirectory); err != nil {
					t.Fatalf("Rename(destination parent) error = %v", err)
				}
				if err := os.Mkdir(destinationDirectory, 0o755); err != nil {
					t.Fatalf("Mkdir(replacement parent) error = %v", err)
				}
				writeBytes(t, destinationDirectory, "marker", []byte("external directory"))
			}
		}
		return baseStatPath(path)
	}

	result, err := publish(t.Context(), Request{
		CandidatePath:   candidate,
		DestinationPath: destination,
	}, operations)
	if !errors.Is(err, ErrCompare) || result != (Result{}) {
		t.Errorf("publish() = %+v/%v, want empty/ErrCompare", result, err)
	}
	if _, err := os.Stat(filepath.Join(destinationDirectory, "stategeo.mmdb")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("replacement parent destination error = %v, want not exist", err)
	}
	if actual := readFile(t, filepath.Join(displacedDirectory, "stategeo.mmdb")); !bytes.Equal(actual, destinationBefore) {
		t.Error("displaced destination changed")
	}
	assertNoTemporaryArtifacts(t, displacedDirectory)
}

func TestPublish_TemporarySwapBeforeVerificationFailsSafely(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
	destination := writeCandidate(t, destinationDirectory, "stategeo.mmdb", testBuildEpoch+1, "GB")
	destinationBefore := readFile(t, destination)
	malicious := writeCandidate(t, destinationDirectory, "malicious.mmdb", testBuildEpoch+2, "CA")
	maliciousBytes := readFile(t, malicious)
	temporaryName := temporaryPrefix + "owned"
	displaced := filepath.Join(destinationDirectory, "displaced.mmdb")
	operations := defaultOperations()
	operations.randomName = func() (string, error) { return temporaryName, nil }
	wrapDestinationRoot(t, destinationDirectory, &operations, func(opened root) root {
		swapped := false
		return &hookRoot{root: opened, open: func(name string) (file, error) {
			if name == temporaryName && !swapped {
				swapped = true
				if err := os.Rename(filepath.Join(destinationDirectory, temporaryName), displaced); err != nil {
					t.Fatalf("Rename(temporary) error = %v", err)
				}
				if err := os.Rename(malicious, filepath.Join(destinationDirectory, temporaryName)); err != nil {
					t.Fatalf("Rename(malicious) error = %v", err)
				}
			}
			return opened.Open(name)
		}}
	})

	result, err := publish(t.Context(), Request{CandidatePath: candidate, DestinationPath: destination}, operations)
	if !errors.Is(err, ErrVerify) || !errors.Is(err, ErrCleanup) || result != (Result{}) {
		t.Errorf("publish() = %+v/%v, want empty/ErrVerify+ErrCleanup", result, err)
	}
	if !bytes.Equal(readFile(t, destination), destinationBefore) {
		t.Error("temporary swap changed destination")
	}
	if !bytes.Equal(readFile(t, filepath.Join(destinationDirectory, temporaryName)), maliciousBytes) {
		t.Error("cleanup deleted or changed the replacement temporary path")
	}
}

func TestPublish_TemporarySwapBeforeRenameFailsSafely(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
	destination := writeCandidate(t, destinationDirectory, "stategeo.mmdb", testBuildEpoch+1, "GB")
	destinationBefore := readFile(t, destination)
	malicious := writeCandidate(t, destinationDirectory, "malicious.mmdb", testBuildEpoch+2, "CA")
	maliciousBytes := readFile(t, malicious)
	temporaryName := temporaryPrefix + "owned"
	displaced := filepath.Join(destinationDirectory, "displaced.mmdb")
	operations := defaultOperations()
	operations.randomName = func() (string, error) { return temporaryName, nil }
	operations.beforeRename = func(context.Context) error {
		if err := os.Rename(filepath.Join(destinationDirectory, temporaryName), displaced); err != nil {
			t.Fatalf("Rename(temporary) error = %v", err)
		}
		if err := os.Rename(malicious, filepath.Join(destinationDirectory, temporaryName)); err != nil {
			t.Fatalf("Rename(malicious) error = %v", err)
		}
		return nil
	}

	result, err := publish(t.Context(), Request{CandidatePath: candidate, DestinationPath: destination}, operations)
	if !errors.Is(err, ErrCompare) || !errors.Is(err, ErrCleanup) || result != (Result{}) {
		t.Errorf("publish() = %+v/%v, want empty/ErrCompare+ErrCleanup", result, err)
	}
	if !bytes.Equal(readFile(t, destination), destinationBefore) {
		t.Error("temporary swap changed destination")
	}
	if !bytes.Equal(readFile(t, filepath.Join(destinationDirectory, temporaryName)), maliciousBytes) {
		t.Error("cleanup deleted or changed the replacement temporary path")
	}
}

func TestPublish_CancellationBeforeCommitLeavesDestinationUnchanged(t *testing.T) {
	tests := []struct {
		name      string
		configure func(context.CancelFunc, *operations)
	}{
		{
			name: "during copy",
			configure: func(cancel context.CancelFunc, operations *operations) {
				baseCopy := operations.copy
				operations.copy = func(ctx context.Context, writer io.Writer, reader io.Reader) (copyResult, error) {
					cancel()
					return baseCopy(ctx, writer, reader)
				}
			},
		},
		{
			name: "during verification",
			configure: func(cancel context.CancelFunc, operations *operations) {
				operations.verify = func(ctx context.Context, _ file) error {
					cancel()
					return ctx.Err()
				}
			},
		},
		{
			name: "during comparison",
			configure: func(cancel context.CancelFunc, operations *operations) {
				baseCompare := operations.compare
				operations.compare = func(ctx context.Context, left io.Reader, right io.Reader) (comparison, error) {
					cancel()
					return baseCompare(ctx, left, right)
				}
			},
		},
		{
			name: "immediately before rename",
			configure: func(cancel context.CancelFunc, operations *operations) {
				operations.beforeRename = func(ctx context.Context) error {
					cancel()
					return ctx.Err()
				}
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidateDirectory := t.TempDir()
			destinationDirectory := t.TempDir()
			candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
			destination := writeCandidate(t, destinationDirectory, "stategeo.mmdb", testBuildEpoch+1, "GB")
			destinationBefore := readFile(t, destination)
			ctx, cancel := context.WithCancel(t.Context())
			operations := defaultOperations()
			test.configure(cancel, &operations)
			result, err := publish(ctx, Request{CandidatePath: candidate, DestinationPath: destination}, operations)
			if !errors.Is(err, context.Canceled) || result != (Result{}) {
				t.Errorf("publish() = %+v/%v, want empty/context.Canceled", result, err)
			}
			if !bytes.Equal(readFile(t, destination), destinationBefore) {
				t.Error("cancellation changed destination")
			}
			assertNoTemporaryArtifacts(t, destinationDirectory)
		})
	}
}

func TestPublish_CancellationAfterRenameDoesNotOverrideSuccess(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
	destination := writeCandidate(t, destinationDirectory, "stategeo.mmdb", testBuildEpoch+1, "GB")
	candidateBytes := readFile(t, candidate)
	ctx, cancel := context.WithCancel(t.Context())
	operations := operationsForDestination(t, destinationDirectory, func(opened root) root {
		return &hookRoot{root: opened, rename: func(oldName, newName string) error {
			if err := opened.Rename(oldName, newName); err != nil {
				return err
			}
			cancel()
			return nil
		}}
	})
	result, err := publish(ctx, Request{CandidatePath: candidate, DestinationPath: destination}, operations)
	if err != nil || result.Action != ActionReplaced || !errors.Is(ctx.Err(), context.Canceled) {
		t.Errorf("publish() = %+v/%v, context = %v", result, err, ctx.Err())
	}
	if !bytes.Equal(readFile(t, destination), candidateBytes) {
		t.Error("committed bytes differ from candidate")
	}
}

func TestPublish_FailureInjectionAndCleanup(t *testing.T) {
	tests := []struct {
		name              string
		configure         func(*testing.T, string, *operations)
		expected          error
		expectedSecondary error
	}{
		{
			name: "temporary creation",
			configure: func(t *testing.T, destinationDirectory string, operations *operations) {
				wrapDestinationRoot(t, destinationDirectory, operations, func(opened root) root {
					return &hookRoot{root: opened, openFile: func(string, int, os.FileMode) (file, error) {
						return nil, errors.New("secret creation failure")
					}}
				})
			},
			expected: ErrWrite,
		},
		{
			name: "copy",
			configure: func(_ *testing.T, _ string, operations *operations) {
				operations.copy = func(context.Context, io.Writer, io.Reader) (copyResult, error) {
					return copyResult{}, errors.New("secret copy failure")
				}
			},
			expected: ErrWrite,
		},
		{
			name: "sync",
			configure: func(t *testing.T, destinationDirectory string, operations *operations) {
				wrapTemporaryFile(t, destinationDirectory, operations, func(opened file) file {
					return &hookFile{file: opened, sync: func() error { return errors.New("secret sync failure") }}
				})
			},
			expected: ErrWrite,
		},
		{
			name: "close",
			configure: func(t *testing.T, destinationDirectory string, operations *operations) {
				wrapTemporaryFile(t, destinationDirectory, operations, func(opened file) file {
					return &hookFile{file: opened, close: func() error {
						_ = opened.Close()
						return errors.New("secret close failure")
					}}
				})
			},
			expected: ErrWrite,
		},
		{
			name: "verification",
			configure: func(_ *testing.T, _ string, operations *operations) {
				operations.verify = func(context.Context, file) error { return artifact.ErrCorrupt }
			},
			expected:          ErrVerify,
			expectedSecondary: artifact.ErrCorrupt,
		},
		{
			name: "comparison",
			configure: func(_ *testing.T, _ string, operations *operations) {
				operations.compare = func(context.Context, io.Reader, io.Reader) (comparison, error) {
					return comparison{}, errors.New("secret comparison failure")
				}
			},
			expected: ErrCompare,
		},
		{
			name: "rename",
			configure: func(t *testing.T, destinationDirectory string, operations *operations) {
				wrapDestinationRoot(t, destinationDirectory, operations, func(opened root) root {
					return &hookRoot{root: opened, rename: func(string, string) error {
						return errors.New("secret rename failure")
					}}
				})
			},
			expected: ErrReplace,
		},
		{
			name: "primary and cleanup",
			configure: func(t *testing.T, destinationDirectory string, operations *operations) {
				operations.compare = func(context.Context, io.Reader, io.Reader) (comparison, error) {
					return comparison{}, errors.New("secret comparison failure")
				}
				wrapDestinationRoot(t, destinationDirectory, operations, func(opened root) root {
					return &hookRoot{root: opened, remove: func(string) error {
						return errors.New("secret cleanup failure")
					}}
				})
			},
			expected:          ErrCompare,
			expectedSecondary: ErrCleanup,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidateDirectory := t.TempDir()
			destinationDirectory := t.TempDir()
			candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
			destination := writeCandidate(t, destinationDirectory, "stategeo.mmdb", testBuildEpoch+1, "GB")
			candidateBefore := readFile(t, candidate)
			destinationBefore := readFile(t, destination)
			operations := defaultOperations()
			test.configure(t, destinationDirectory, &operations)
			_, err := publish(t.Context(), Request{CandidatePath: candidate, DestinationPath: destination}, operations)
			if !errors.Is(err, test.expected) {
				t.Errorf("publish() error = %v, want errors.Is(%v)", err, test.expected)
			}
			if test.expectedSecondary != nil && !errors.Is(err, test.expectedSecondary) {
				t.Errorf("publish() error = %v, want errors.Is(%v)", err, test.expectedSecondary)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), candidate) || strings.Contains(err.Error(), destination) {
				t.Errorf("publish() error leaked detail: %v", err)
			}
			assertCandidateUnchanged(t, candidate, candidateBefore)
			if !bytes.Equal(readFile(t, destination), destinationBefore) {
				t.Error("injected failure changed destination")
			}
			if test.name != "primary and cleanup" {
				assertNoTemporaryArtifacts(t, destinationDirectory)
			}
		})
	}
}

func TestPublish_RetriesBoundedRandomNameCollision(t *testing.T) {
	candidateDirectory := t.TempDir()
	destinationDirectory := t.TempDir()
	candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
	destination := filepath.Join(destinationDirectory, "stategeo.mmdb")
	collisionName := temporaryPrefix + "collision"
	writeBytes(t, destinationDirectory, collisionName, []byte("occupied"))
	operations := defaultOperations()
	attempts := 0
	operations.randomName = func() (string, error) {
		attempts++
		if attempts < nameAttempts {
			return collisionName, nil
		}
		return temporaryPrefix + "final", nil
	}
	result, err := publish(t.Context(), Request{CandidatePath: candidate, DestinationPath: destination}, operations)
	if err != nil || result.Action != ActionCreated || attempts != nameAttempts {
		t.Errorf("publish() = %+v/%v, attempts %d", result, err, attempts)
	}
}

func TestPublish_RejectsInvalidCandidateWithoutChangingDestination(t *testing.T) {
	tests := []struct {
		name      string
		candidate func(*testing.T, string) string
		expected  error
		secondary error
	}{
		{name: "missing", candidate: func(_ *testing.T, directory string) string { return filepath.Join(directory, "missing.mmdb") }, expected: ErrCandidate},
		{name: "directory", candidate: func(_ *testing.T, directory string) string { return directory }, expected: ErrCandidate},
		{name: "symlink", candidate: candidateSymlink, expected: ErrCandidate},
		{name: "named pipe", candidate: candidateNamedPipe, expected: ErrCandidate},
		{name: "corrupt", candidate: func(t *testing.T, directory string) string {
			return writeBytes(t, directory, "candidate.mmdb", []byte("not an mmdb"))
		}, expected: ErrVerify, secondary: artifact.ErrCorrupt},
		{name: "source city", candidate: func(_ *testing.T, _ string) string {
			return filepath.Join("..", "..", "testdata", "maxmind", "GeoIP2-City-Test.mmdb")
		}, expected: ErrVerify, secondary: artifact.ErrUnsupported},
		{name: "incompatible schema", candidate: incompatibleCandidate, expected: ErrVerify, secondary: artifact.ErrUnsupported},
		{name: "wrong country type", candidate: func(t *testing.T, directory string) string {
			return runtimeIncompatibleCandidate(t, directory, mmdbtype.Map{
				"country": mmdbtype.Map{"iso_code": mmdbtype.Uint32(1)},
			})
		}, expected: ErrVerify, secondary: artifact.ErrUnsupported},
		{name: "invalid country code", candidate: func(t *testing.T, directory string) string {
			return runtimeIncompatibleCandidate(t, directory, mmdbtype.Map{
				"country": mmdbtype.Map{"iso_code": mmdbtype.String("USA")},
			})
		}, expected: ErrVerify, secondary: artifact.ErrUnsupported},
		{name: "invalid subdivision code", candidate: func(t *testing.T, directory string) string {
			return runtimeIncompatibleCandidate(t, directory, mmdbtype.Map{
				"country": mmdbtype.Map{"iso_code": mmdbtype.String("US")},
				"subdivisions": mmdbtype.Slice{
					mmdbtype.Map{"iso_code": mmdbtype.String("CALI")},
				},
			})
		}, expected: ErrVerify, secondary: artifact.ErrUnsupported},
		{name: "empty", candidate: func(t *testing.T, directory string) string {
			return writeBytes(t, directory, "candidate.mmdb", []byte{})
		}, expected: ErrCandidate},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidateDirectory := t.TempDir()
			destinationDirectory := t.TempDir()
			candidate := test.candidate(t, candidateDirectory)
			destination := writeCandidate(t, destinationDirectory, "stategeo.mmdb", testBuildEpoch, "US")
			destinationBefore := readFile(t, destination)
			_, err := Publish(t.Context(), Request{CandidatePath: candidate, DestinationPath: destination})
			if !errors.Is(err, test.expected) {
				t.Errorf("Publish() error = %v, want errors.Is(%v)", err, test.expected)
			}
			if test.secondary != nil && !errors.Is(err, test.secondary) {
				t.Errorf("Publish() error = %v, want errors.Is(%v)", err, test.secondary)
			}
			if !bytes.Equal(readFile(t, destination), destinationBefore) {
				t.Error("rejected candidate changed destination")
			}
			assertNoPublicationArtifacts(t, destinationDirectory, "stategeo.mmdb")
		})
	}
}

func TestPublish_RejectsInvalidDestination(t *testing.T) {
	tests := []struct {
		name        string
		destination func(*testing.T, string) string
	}{
		{name: "missing parent", destination: func(_ *testing.T, directory string) string {
			return filepath.Join(directory, "missing", "stategeo.mmdb")
		}},
		{name: "parent is file", destination: func(t *testing.T, directory string) string {
			parent := writeBytes(t, directory, "parent", []byte("file"))
			return filepath.Join(parent, "stategeo.mmdb")
		}},
		{name: "directory", destination: func(_ *testing.T, directory string) string { return directory }},
		{name: "symlink", destination: destinationSymlink},
		{name: "named pipe", destination: candidateNamedPipe},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidateDirectory := t.TempDir()
			destinationDirectory := t.TempDir()
			candidate := writeCandidate(t, candidateDirectory, "candidate.mmdb", testBuildEpoch, "US")
			candidateBefore := readFile(t, candidate)
			destination := test.destination(t, destinationDirectory)
			_, err := Publish(t.Context(), Request{CandidatePath: candidate, DestinationPath: destination})
			if !errors.Is(err, ErrDestination) && !errors.Is(err, ErrInvalidRequest) {
				t.Errorf("Publish() error = %v, want destination or request classification", err)
			}
			assertCandidateUnchanged(t, candidate, candidateBefore)
			assertNoTemporaryArtifacts(t, destinationDirectory)
		})
	}
}

func TestPublish_ValidatesRequestAndPlatformBeforeFilesystemWork(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	opened := 0
	operations := defaultOperations()
	operations.openRoot = func(string) (root, error) {
		opened++
		return nil, errors.New("unexpected open")
	}
	_, err := publish(ctx, Request{CandidatePath: "candidate", DestinationPath: "destination"}, operations)
	if !errors.Is(err, context.Canceled) || opened != 0 {
		t.Errorf("pre-cancelled publish = %v, opens %d", err, opened)
	}

	operations.goos = "windows"
	_, err = publish(t.Context(), Request{CandidatePath: "candidate", DestinationPath: "destination"}, operations)
	if !errors.Is(err, ErrUnsupportedPlatform) || opened != 0 {
		t.Errorf("unsupported publish = %v, opens %d", err, opened)
	}

	invalid := []Request{
		{},
		{CandidatePath: "candidate", DestinationPath: ""},
		{CandidatePath: "candidate", DestinationPath: "/"},
		{CandidatePath: "candidate", DestinationPath: "directory/."},
		{CandidatePath: "candidate", DestinationPath: "directory/.."},
	}
	operations.goos = runtime.GOOS
	for _, request := range invalid {
		_, err = publish(t.Context(), request, operations)
		if !errors.Is(err, ErrInvalidRequest) || opened != 0 {
			t.Errorf("publish(%#v) = %v, opens %d", request, err, opened)
		}
	}
	if _, err := Publish(nil, Request{CandidatePath: "candidate", DestinationPath: "destination"}); !errors.Is(err, ErrInvalidRequest) {
		t.Errorf("Publish(nil) error = %v, want ErrInvalidRequest", err)
	}
}

func writeCandidate(t *testing.T, directory, name string, epoch int64, country string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("OpenFile(candidate) error = %v", err)
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
		t.Fatalf("Close(candidate) error = %v", err)
	}
	return path
}

func writeCompatibleCandidate(
	t *testing.T,
	directory string,
	name string,
	epoch int64,
	country string,
	recordSize int,
) string {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch:              epoch,
		DatabaseType:            mmdb.DatabaseType,
		Description:             map[string]string{"en": mmdb.SchemaDescription},
		IncludeReservedNetworks: true,
		IPVersion:               6,
		Languages:               []string{},
		RecordSize:              recordSize,
	})
	if err != nil {
		t.Fatalf("mmdbwriter.New() error = %v", err)
	}
	value := mmdbtype.Map{
		"country": mmdbtype.Map{"iso_code": mmdbtype.String(country)},
	}
	if err := tree.Insert(prefixNetwork(netip.MustParsePrefix("192.0.2.0/24")), value); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	path := filepath.Join(directory, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := tree.WriteTo(file); err != nil {
		_ = file.Close()
		t.Fatalf("WriteTo() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return path
}

func incompatibleCandidate(t *testing.T, directory string) string {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch:              testBuildEpoch,
		DatabaseType:            mmdb.DatabaseType,
		Description:             map[string]string{"en": "stategeodb incompatible schema"},
		IncludeReservedNetworks: true,
		IPVersion:               6,
		Languages:               []string{},
		RecordSize:              28,
	})
	if err != nil {
		t.Fatalf("mmdbwriter.New() error = %v", err)
	}
	value := mmdbtype.Map{"country": mmdbtype.Map{"iso_code": mmdbtype.String("US")}}
	if err := tree.Insert(prefixNetwork(netip.MustParsePrefix("192.0.2.0/24")), value); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	path := filepath.Join(directory, "candidate.mmdb")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := tree.WriteTo(file); err != nil {
		_ = file.Close()
		t.Fatalf("WriteTo() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return path
}

func runtimeIncompatibleCandidate(
	t *testing.T,
	directory string,
	value mmdbtype.DataType,
) string {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch:              testBuildEpoch,
		DatabaseType:            mmdb.DatabaseType,
		Description:             map[string]string{"en": mmdb.SchemaDescription},
		IncludeReservedNetworks: true,
		IPVersion:               6,
		Languages:               []string{},
		RecordSize:              28,
	})
	if err != nil {
		t.Fatalf("mmdbwriter.New() error = %v", err)
	}
	if err := tree.Insert(prefixNetwork(netip.MustParsePrefix("192.0.2.0/24")), value); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	path := filepath.Join(directory, "candidate.mmdb")
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatalf("OpenFile() error = %v", err)
	}
	if _, err := tree.WriteTo(file); err != nil {
		_ = file.Close()
		t.Fatalf("WriteTo() error = %v", err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return path
}

func prefixNetwork(prefix netip.Prefix) *net.IPNet {
	address := prefix.Addr().As4()
	return &net.IPNet{IP: net.IP(address[:]), Mask: net.CIDRMask(prefix.Bits(), 32)}
}

func candidateSymlink(t *testing.T, directory string) string {
	t.Helper()
	target := writeCandidate(t, directory, "target.mmdb", testBuildEpoch, "US")
	path := filepath.Join(directory, "candidate.mmdb")
	if err := os.Symlink(filepath.Base(target), path); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	return path
}

func destinationSymlink(t *testing.T, directory string) string {
	t.Helper()
	target := writeCandidate(t, directory, "target.mmdb", testBuildEpoch, "US")
	path := filepath.Join(directory, "stategeo.mmdb")
	if err := os.Symlink(filepath.Base(target), path); err != nil {
		t.Fatalf("Symlink() error = %v", err)
	}
	return path
}

func candidateNamedPipe(t *testing.T, directory string) string {
	t.Helper()
	path := filepath.Join(directory, "candidate.pipe")
	if err := syscall.Mkfifo(path, 0o600); err != nil {
		t.Fatalf("Mkfifo() error = %v", err)
	}
	return path
}

func writeBytes(t *testing.T, directory, name string, contents []byte) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	return path
}

func readFile(t *testing.T, path string) []byte {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	return contents
}

func assertCandidateUnchanged(t *testing.T, path string, expected []byte) {
	t.Helper()
	if actual := readFile(t, path); !bytes.Equal(actual, expected) {
		t.Error("candidate bytes changed")
	}
}

func assertNoPublicationArtifacts(t *testing.T, directory string, allowed ...string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	allowedNames := make(map[string]bool, len(allowed))
	for _, name := range allowed {
		allowedNames[name] = true
	}
	for _, entry := range entries {
		if !allowedNames[entry.Name()] {
			t.Errorf("unexpected publication artifact %q", entry.Name())
		}
	}
}

func assertNoTemporaryArtifacts(t *testing.T, directory string) {
	t.Helper()
	entries, err := os.ReadDir(directory)
	if err != nil {
		t.Fatalf("ReadDir() error = %v", err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), temporaryPrefix) {
			t.Errorf("temporary artifact remained: %q", entry.Name())
		}
	}
}

type hookRoot struct {
	root
	open     func(string) (file, error)
	openFile func(string, int, os.FileMode) (file, error)
	remove   func(string) error
	rename   func(string, string) error
}

type maximumChunkReader struct {
	reader  io.Reader
	maximum int
}

type cancelAfterReadReader struct {
	reader io.Reader
	cancel context.CancelFunc
	read   bool
}

func (reader *cancelAfterReadReader) Read(buffer []byte) (int, error) {
	read, err := reader.reader.Read(buffer)
	if !reader.read {
		reader.read = true
		reader.cancel()
	}
	return read, err
}

type zeroProgressReader struct{}

func (zeroProgressReader) Read([]byte) (int, error) {
	return 0, nil
}

func (reader *maximumChunkReader) Read(buffer []byte) (int, error) {
	if len(buffer) > reader.maximum {
		buffer = buffer[:reader.maximum]
	}
	return reader.reader.Read(buffer)
}

func (root *hookRoot) Open(name string) (file, error) {
	if root.open != nil {
		return root.open(name)
	}
	return root.root.Open(name)
}

func (root *hookRoot) OpenFile(name string, flag int, mode os.FileMode) (file, error) {
	if root.openFile != nil {
		return root.openFile(name, flag, mode)
	}
	return root.root.OpenFile(name, flag, mode)
}

func (root *hookRoot) Remove(name string) error {
	if root.remove != nil {
		return root.remove(name)
	}
	return root.root.Remove(name)
}

func (root *hookRoot) Rename(oldName string, newName string) error {
	if root.rename != nil {
		return root.rename(oldName, newName)
	}
	return root.root.Rename(oldName, newName)
}

type hookFile struct {
	file
	sync  func() error
	close func() error
}

func (file *hookFile) Sync() error {
	if file.sync != nil {
		return file.sync()
	}
	return file.file.Sync()
}

func (file *hookFile) Close() error {
	if file.close != nil {
		return file.close()
	}
	return file.file.Close()
}

func operationsForDestination(
	t *testing.T,
	destinationDirectory string,
	wrap func(root) root,
) operations {
	t.Helper()
	operations := defaultOperations()
	wrapDestinationRoot(t, destinationDirectory, &operations, wrap)
	return operations
}

func wrapDestinationRoot(
	t *testing.T,
	destinationDirectory string,
	operations *operations,
	wrap func(root) root,
) {
	t.Helper()
	baseOpenRoot := operations.openRoot
	destinationParent := destinationDirectory + string(os.PathSeparator)
	operations.openRoot = func(path string) (root, error) {
		opened, err := baseOpenRoot(path)
		if err != nil || path != destinationParent {
			return opened, err
		}
		return wrap(opened), nil
	}
}

func wrapTemporaryFile(
	t *testing.T,
	destinationDirectory string,
	operations *operations,
	wrap func(file) file,
) {
	t.Helper()
	wrapDestinationRoot(t, destinationDirectory, operations, func(opened root) root {
		return &hookRoot{root: opened, openFile: func(name string, flag int, mode os.FileMode) (file, error) {
			created, err := opened.OpenFile(name, flag, mode)
			if err != nil {
				return nil, err
			}
			if strings.HasPrefix(name, temporaryPrefix) {
				return wrap(created), nil
			}
			return created, nil
		}}
	})
}

var _ root = (*hookRoot)(nil)
var _ file = (*hookFile)(nil)
