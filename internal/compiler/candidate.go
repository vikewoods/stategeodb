package compiler

import (
	"errors"
	"os"
)

// EquivalenceStats is bounded evidence from an exact source/candidate
// behavioral comparison. Network counts may differ when behavior is split or
// compacted across different prefix shapes.
type EquivalenceStats struct {
	SourceRecords    int
	OutputNetworks   int
	ComparedSegments int
}

// Candidate owns one verified generated workspace and its candidate MMDB.
// Candidate values must remain pointer-owned. Cleanup and other methods are
// safe for sequential use; concurrent mutation is not supported.
type Candidate struct {
	workspaceName    string
	candidatePath    string
	inputRecordCount int
	size             int64
	buildEpoch       int64
	equivalenceStats EquivalenceStats
	profileStats     projectionStats
	rootPath         string
	rootInfo         os.FileInfo
	candidateName    string
	candidateInfo    os.FileInfo
	openRoot         func(string, os.FileInfo) (workspaceRoot, error)
	rootLstat        func(workspaceRoot, string) (os.FileInfo, error)
	removeWorkspace  func(workspaceRoot, string) error
	closeRoot        func(workspaceRoot) error
	cleaned          bool
}

// Path returns the candidate MMDB path while the configured root and candidate
// still identify the verified filesystem objects. It returns an empty string
// if either pathname has been replaced. After Cleanup, it returns the original
// path so callers can confirm that the artifact no longer exists.
func (candidate *Candidate) Path() string {
	if candidate == nil {
		return ""
	}
	if candidate.cleaned {
		return candidate.candidatePath
	}

	root, err := candidate.openRoot(candidate.rootPath, candidate.rootInfo)
	if err != nil {
		return ""
	}
	info, statErr := candidate.rootLstat(root, candidate.candidateName)
	closeErr := candidate.closeRoot(root)
	if statErr != nil || closeErr != nil || !os.SameFile(candidate.candidateInfo, info) {
		return ""
	}
	return candidate.candidatePath
}

// InputRecordCount returns the normalized record count supplied to the writer.
func (candidate *Candidate) InputRecordCount() int {
	if candidate == nil {
		return 0
	}
	return candidate.inputRecordCount
}

// Size returns the exact verified candidate file size in bytes.
func (candidate *Candidate) Size() int64 {
	if candidate == nil {
		return 0
	}
	return candidate.size
}

// BuildEpoch returns the caller-supplied build epoch encoded in the candidate.
func (candidate *Candidate) BuildEpoch() int64 {
	if candidate == nil {
		return 0
	}
	return candidate.buildEpoch
}

// EquivalenceStats returns a copy of the successful exact-comparison evidence.
func (candidate *Candidate) EquivalenceStats() EquivalenceStats {
	if candidate == nil {
		return EquivalenceStats{}
	}
	return candidate.equivalenceStats
}

// Cleanup removes the generated workspace name within candidate's bound root.
// The root must remain exclusively controlled by the caller because the child
// workspace identity is not rebound at cleanup. Repeated sequential calls are
// harmless. A failed removal remains retryable.
func (candidate *Candidate) Cleanup() error {
	if candidate == nil || candidate.workspaceName == "" {
		return nil
	}

	root, err := candidate.openRoot(candidate.rootPath, candidate.rootInfo)
	if err != nil {
		return classified("open candidate workspace root", ErrCleanup)
	}
	if err := candidate.removeWorkspace(root, candidate.workspaceName); err != nil {
		if closeErr := candidate.closeRoot(root); closeErr != nil {
			return errors.Join(
				classified("remove candidate workspace", ErrCleanup),
				classified("close candidate workspace root", ErrCleanup),
			)
		}
		return classified("remove candidate workspace", ErrCleanup)
	}

	closeErr := candidate.closeRoot(root)
	candidate.workspaceName = ""
	candidate.cleaned = true
	if closeErr != nil {
		return classified("close candidate workspace root", ErrCleanup)
	}
	return nil
}
