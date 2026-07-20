// Package compiler orchestrates verified single-source candidate builds.
package compiler

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/vikewoods/stategeodb/internal/artifact"
	"github.com/vikewoods/stategeodb/internal/mmdb"
	"github.com/vikewoods/stategeodb/internal/source"
	"github.com/vikewoods/stategeodb/internal/source/maxmind"
)

const (
	workspacePrefix = ".stategeodb-build-"
	candidateName   = "candidate.mmdb"
)

var (
	// ErrInvalidRequest classifies missing or invalid compile inputs.
	ErrInvalidRequest = errors.New("compiler: invalid request")
	// ErrWorkspace classifies private workspace and candidate-file failures.
	ErrWorkspace = errors.New("compiler: workspace failure")
	// ErrVerify classifies candidate open, structure, or metadata failures.
	ErrVerify = errors.New("compiler: candidate verification failure")
	// ErrProfile classifies failure to project normalized source records into
	// the fixed compliance artifact profile.
	ErrProfile = errors.New("compiler: artifact profile failure")
	// ErrCleanup classifies failure to remove an owned generated workspace.
	ErrCleanup           = errors.New("compiler: cleanup failure")
	errCandidateIdentity = errors.New("compiler: candidate identity changed")
)

// Request describes one deterministic compile invocation.
type Request struct {
	SourcePath    string
	SourceID      string
	WorkspaceRoot string
	BuildEpoch    int64
}

// Compile ingests one verified City source and returns ownership of one fully
// written, structurally verified, and behaviorally equivalent candidate
// workspace. On error, Compile returns no candidate and attempts to remove
// every workspace it created.
//
// Cancellation is observed around upstream open, record projection, write,
// file synchronization, verification, traversal, interval validation, and
// comparison. One active upstream call or standard-library sort cannot be
// interrupted safely.
func Compile(ctx context.Context, request Request) (*Candidate, error) {
	return compile(ctx, request, defaultOperations())
}

func compile(
	ctx context.Context,
	request Request,
	operations compilerOperations,
) (*Candidate, error) {
	rootPath, rootInfo, err := validateRequest(ctx, request, operations.lstat)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	root, err := operations.openRoot(rootPath, rootInfo)
	if err != nil {
		return nil, classified("open workspace root", ErrInvalidRequest)
	}
	closeBeforeWorkspace := func(primary error) error {
		return closeRootAfterFailure(root, primary, operations.closeRoot)
	}

	database, err := operations.openSource(request.SourcePath)
	if err != nil {
		return nil, closeBeforeWorkspace(err)
	}

	records, ingestErr := database.Records(ctx, request.SourceID)
	closeErr := database.Close()
	if ingestErr != nil {
		return nil, closeBeforeWorkspace(errors.Join(ingestErr, closeErr))
	}
	if closeErr != nil {
		return nil, closeBeforeWorkspace(closeErr)
	}
	profileStats, err := operations.project(ctx, records)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, closeBeforeWorkspace(err)
		}
		return nil, closeBeforeWorkspace(classifiedWithCause(
			"project source records",
			ErrProfile,
			err,
		))
	}
	if err := ctx.Err(); err != nil {
		return nil, closeBeforeWorkspace(err)
	}
	if !rootStillMatches(rootPath, rootInfo, operations.lstat) {
		return nil, closeBeforeWorkspace(classified("validate workspace root identity", ErrWorkspace))
	}

	workspaceName, err := operations.mkdirTemp(root, workspacePrefix)
	if err != nil {
		return nil, closeBeforeWorkspace(classified("create candidate workspace", ErrWorkspace))
	}
	fail := func(primary error) (*Candidate, error) {
		return nil, cleanupAfterFailure(
			root,
			workspaceName,
			primary,
			operations.removeAll,
			operations.closeRoot,
		)
	}
	if !rootStillMatches(rootPath, rootInfo, operations.lstat) {
		return fail(classified("validate workspace root identity", ErrWorkspace))
	}

	if err := operations.chmod(root, workspaceName, 0o700); err != nil {
		return fail(classified("secure candidate workspace", ErrWorkspace))
	}
	workspaceInfo, err := operations.rootLstat(root, workspaceName)
	if err != nil || !workspaceInfo.IsDir() || workspaceInfo.Mode().Perm() != 0o700 {
		return fail(classified("validate candidate workspace", ErrWorkspace))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}

	candidateNameRelative := filepath.Join(workspaceName, candidateName)
	candidatePath := filepath.Join(rootPath, candidateNameRelative)
	file, err := operations.openFile(
		root,
		candidateNameRelative,
		os.O_WRONLY|os.O_CREATE|os.O_EXCL,
		0o600,
	)
	if err != nil {
		return fail(classified("create candidate file", ErrWorkspace))
	}
	if err := file.Chmod(0o600); err != nil {
		return fail(closeFileAfterFailure(
			file,
			classified("secure candidate file", ErrWorkspace),
		))
	}
	if err := ctx.Err(); err != nil {
		return fail(closeFileAfterFailure(file, err))
	}

	written, err := operations.write(file, records, mmdb.Options{
		BuildEpoch: request.BuildEpoch,
	})
	if err != nil {
		return fail(closeFileAfterFailure(file, err))
	}
	if written <= 0 {
		return fail(closeFileAfterFailure(
			file,
			classified("validate writer byte count", ErrWorkspace),
		))
	}
	if err := file.Sync(); err != nil {
		return fail(closeFileAfterFailure(
			file,
			classified("synchronize candidate file", ErrWorkspace),
		))
	}
	writtenInfo, err := file.Stat()
	if err != nil {
		return fail(closeFileAfterFailure(
			file,
			classified("stat open candidate file", ErrWorkspace),
		))
	}
	isExpectedOpenFile := writtenInfo.Mode().IsRegular() &&
		writtenInfo.Mode().Perm() == 0o600 &&
		writtenInfo.Size() == written
	if !isExpectedOpenFile {
		return fail(closeFileAfterFailure(
			file,
			classified("validate open candidate file", ErrWorkspace),
		))
	}
	if err := file.Close(); err != nil {
		return fail(classified("close candidate file", ErrWorkspace))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}

	contents, err := operations.readFile(root, candidateNameRelative, writtenInfo)
	if err != nil {
		if errors.Is(err, errCandidateIdentity) {
			return fail(classified("validate candidate identity", ErrNotEquivalent))
		}
		return fail(classified("open candidate database", ErrVerify))
	}
	equivalenceStats, err := verifyCandidate(
		ctx,
		contents,
		request.BuildEpoch,
		request.SourceID,
		records,
		operations.openVerification,
		operations.compare,
	)
	if err != nil {
		return fail(err)
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}

	info, err := operations.rootLstat(root, candidateNameRelative)
	if err != nil {
		return fail(classified("stat candidate file", ErrWorkspace))
	}
	isExpectedFile := info.Mode().IsRegular() &&
		info.Mode().Perm() == 0o600 &&
		os.SameFile(writtenInfo, info)
	if !isExpectedFile || info.Size() <= 0 || info.Size() != written {
		return fail(classified("validate candidate file", ErrWorkspace))
	}
	if err := ctx.Err(); err != nil {
		return fail(err)
	}
	if !rootStillMatches(rootPath, rootInfo, operations.lstat) {
		return fail(classified("validate workspace root identity", ErrWorkspace))
	}
	if err := operations.closeRoot(root); err != nil {
		return fail(classified("close candidate workspace root", ErrWorkspace))
	}

	return &Candidate{
		workspaceName:    workspaceName,
		candidatePath:    candidatePath,
		inputRecordCount: len(records),
		size:             info.Size(),
		buildEpoch:       request.BuildEpoch,
		equivalenceStats: equivalenceStats,
		profileStats:     profileStats,
		rootPath:         rootPath,
		rootInfo:         rootInfo,
		candidateName:    candidateNameRelative,
		candidateInfo:    info,
		openRoot:         operations.openRoot,
		rootLstat:        operations.rootLstat,
		removeWorkspace:  operations.removeAll,
		closeRoot:        operations.closeRoot,
	}, nil
}

func rootStillMatches(
	path string,
	expected os.FileInfo,
	lstat func(string) (os.FileInfo, error),
) bool {
	actual, err := lstat(path)
	return err == nil && actual.IsDir() && os.SameFile(expected, actual)
}

func validateRequest(
	ctx context.Context,
	request Request,
	lstat func(string) (os.FileInfo, error),
) (string, os.FileInfo, error) {
	if ctx == nil {
		return "", nil, classified("validate context", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return "", nil, err
	}
	if request.SourcePath == "" {
		return "", nil, classified("validate source path", ErrInvalidRequest)
	}
	if err := source.ValidateSourceID(request.SourceID); err != nil {
		return "", nil, classifiedWithCause("validate source id", ErrInvalidRequest, err)
	}
	if request.WorkspaceRoot == "" {
		return "", nil, classified("validate workspace root", ErrInvalidRequest)
	}
	if !filepath.IsAbs(request.WorkspaceRoot) {
		return "", nil, classified("validate workspace root", ErrInvalidRequest)
	}
	if request.BuildEpoch <= 0 {
		return "", nil, classified("validate build epoch", ErrInvalidRequest)
	}

	info, err := lstat(request.WorkspaceRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, classified("validate workspace root", ErrInvalidRequest)
	}
	return filepath.Clean(request.WorkspaceRoot), info, nil
}

func cleanupAfterFailure(
	root workspaceRoot,
	workspaceName string,
	primary error,
	removeAll func(workspaceRoot, string) error,
	closeRoot func(workspaceRoot) error,
) error {
	var cleanupErr error
	if err := removeAll(root, workspaceName); err != nil {
		cleanupErr = classified("remove failed candidate workspace", ErrCleanup)
	}
	if err := closeRoot(root); err != nil {
		cleanupErr = errors.Join(
			cleanupErr,
			classified("close failed workspace root", ErrCleanup),
		)
	}
	return errors.Join(primary, cleanupErr)
}

func closeRootAfterFailure(
	root workspaceRoot,
	primary error,
	closeRoot func(workspaceRoot) error,
) error {
	if err := closeRoot(root); err != nil {
		return errors.Join(primary, classified("close workspace root", ErrWorkspace))
	}
	return primary
}

func closeFileAfterFailure(file candidateFile, primary error) error {
	if err := file.Close(); err != nil {
		return errors.Join(primary, classified("close failed candidate file", ErrWorkspace))
	}
	return primary
}

type sourceDatabase interface {
	Records(context.Context, string) ([]source.Record, error)
	Close() error
}

type candidateFile interface {
	io.Writer
	Chmod(os.FileMode) error
	Sync() error
	Stat() (os.FileInfo, error)
	Close() error
}

type verificationDatabase interface {
	Verify() error
	Metadata() maxminddb.Metadata
	Networks() networkIterator
	Close() error
}

type workspaceRoot interface {
	Chmod(string, os.FileMode) error
	Close() error
	Lstat(string) (os.FileInfo, error)
	Mkdir(string, os.FileMode) error
	OpenFile(string, int, os.FileMode) (*os.File, error)
	RemoveAll(string) error
	Rename(string, string) error
}

type compilerOperations struct {
	lstat            func(string) (os.FileInfo, error)
	openRoot         func(string, os.FileInfo) (workspaceRoot, error)
	openSource       func(string) (sourceDatabase, error)
	project          func(context.Context, []source.Record) (projectionStats, error)
	mkdirTemp        func(workspaceRoot, string) (string, error)
	chmod            func(workspaceRoot, string, os.FileMode) error
	rootLstat        func(workspaceRoot, string) (os.FileInfo, error)
	openFile         func(workspaceRoot, string, int, os.FileMode) (candidateFile, error)
	write            func(io.Writer, []source.Record, mmdb.Options) (int64, error)
	readFile         func(workspaceRoot, string, os.FileInfo) ([]byte, error)
	openVerification func([]byte) (verificationDatabase, error)
	compare          func(context.Context, []source.Record, []source.Record) (EquivalenceStats, error)
	removeAll        func(workspaceRoot, string) error
	closeRoot        func(workspaceRoot) error
}

func defaultOperations() compilerOperations {
	return compilerOperations{
		lstat:      os.Lstat,
		openRoot:   openWorkspaceRoot,
		openSource: openSource,
		project:    projectRecords,
		mkdirTemp:  createWorkspace,
		chmod: func(root workspaceRoot, name string, mode os.FileMode) error {
			return root.Chmod(name, mode)
		},
		rootLstat: func(root workspaceRoot, name string) (os.FileInfo, error) {
			return root.Lstat(name)
		},
		openFile: func(root workspaceRoot, name string, flags int, mode os.FileMode) (candidateFile, error) {
			return root.OpenFile(name, flags, mode)
		},
		write:    mmdb.Write,
		readFile: readOwnedCandidate,
		openVerification: func(contents []byte) (verificationDatabase, error) {
			reader, err := maxminddb.OpenBytes(contents)
			if err != nil {
				return nil, err
			}
			return &upstreamVerificationDatabase{reader: reader}, nil
		},
		compare: compareRecordBehavior,
		removeAll: func(root workspaceRoot, name string) error {
			return root.RemoveAll(name)
		},
		closeRoot: func(root workspaceRoot) error {
			return root.Close()
		},
	}
}

func openSource(path string) (sourceDatabase, error) {
	return maxmind.Open(path)
}

func openWorkspaceRoot(path string, expected os.FileInfo) (workspaceRoot, error) {
	root, err := os.OpenRoot(path)
	if err != nil {
		return nil, err
	}
	actual, err := root.Lstat(".")
	if err != nil || !os.SameFile(expected, actual) {
		_ = root.Close()
		return nil, errors.New("workspace root changed during validation")
	}
	return root, nil
}

func readOwnedCandidate(
	root workspaceRoot,
	name string,
	expected os.FileInfo,
) ([]byte, error) {
	file, err := root.OpenFile(name, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	actual, statErr := file.Stat()
	isExpectedFile := statErr == nil &&
		actual.Mode().IsRegular() &&
		actual.Mode().Perm() == 0o600 &&
		actual.Size() == expected.Size() &&
		os.SameFile(expected, actual)
	if !isExpectedFile {
		return nil, errors.Join(errCandidateIdentity, file.Close())
	}

	size := expected.Size()
	if size < 0 || uint64(size) > uint64(^uint(0)>>1) {
		return nil, errors.Join(errCandidateIdentity, file.Close())
	}
	contents := make([]byte, int(size))
	_, readErr := io.ReadFull(file, contents)
	closeErr := file.Close()
	if errors.Is(readErr, io.ErrUnexpectedEOF) {
		return nil, errors.Join(errCandidateIdentity, closeErr)
	}
	if readErr != nil || closeErr != nil {
		return nil, errors.Join(readErr, closeErr)
	}
	return contents, nil
}

func createWorkspace(root workspaceRoot, prefix string) (string, error) {
	var random [16]byte
	for range 100 {
		if _, err := rand.Read(random[:]); err != nil {
			return "", err
		}
		name := prefix + hex.EncodeToString(random[:])
		if err := root.Mkdir(name, 0o700); err == nil {
			return name, nil
		} else if !errors.Is(err, os.ErrExist) {
			return "", err
		}
	}
	return "", errors.New("exhausted candidate workspace names")
}

type upstreamVerificationDatabase struct {
	reader *maxminddb.Reader
}

func (database *upstreamVerificationDatabase) Verify() error {
	return database.reader.Verify()
}

func (database *upstreamVerificationDatabase) Metadata() maxminddb.Metadata {
	return database.reader.Metadata
}

func (database *upstreamVerificationDatabase) Networks() networkIterator {
	return func(yield func(networkResult) bool) {
		for result := range database.reader.Networks() {
			if !yield(result) {
				return
			}
		}
	}
}

func (database *upstreamVerificationDatabase) Close() error {
	return database.reader.Close()
}

func verifyCandidate(
	ctx context.Context,
	contents []byte,
	buildEpoch int64,
	sourceID string,
	sourceRecords []source.Record,
	open func([]byte) (verificationDatabase, error),
	compare func(context.Context, []source.Record, []source.Record) (EquivalenceStats, error),
) (EquivalenceStats, error) {
	if err := ctx.Err(); err != nil {
		return EquivalenceStats{}, err
	}
	database, err := open(contents)
	if err != nil {
		return EquivalenceStats{}, classified("open candidate database", ErrVerify)
	}

	var primary error
	var stats EquivalenceStats
	if err := ctx.Err(); err != nil {
		primary = err
	} else if err := database.Verify(); err != nil {
		primary = classified("verify candidate structure", ErrVerify)
	} else if !metadataMatches(database.Metadata(), buildEpoch) {
		primary = classified("verify candidate metadata", ErrVerify)
	} else if outputRecords, err := readCandidateRecords(ctx, database.Networks(), sourceID); err != nil {
		primary = err
	} else {
		stats, primary = compare(ctx, sourceRecords, outputRecords)
	}
	if primary == nil {
		primary = ctx.Err()
	}
	if err := database.Close(); err != nil {
		closeErr := classified("close candidate verifier", ErrVerify)
		primary = errors.Join(primary, closeErr)
	}
	if primary != nil {
		return EquivalenceStats{}, primary
	}
	return stats, nil
}

func metadataMatches(metadata maxminddb.Metadata, buildEpoch int64) bool {
	return artifact.Compatible(metadata) &&
		uint64(metadata.BuildEpoch) == uint64(buildEpoch)
}

func classified(operation string, class error) error {
	return &classifiedError{
		operation: operation,
		causes:    []error{class},
	}
}

func classifiedWithCause(operation string, class error, cause error) error {
	return &classifiedError{
		operation: operation,
		causes:    []error{class, cause},
	}
}

type classifiedError struct {
	operation string
	causes    []error
}

func (err *classifiedError) Error() string {
	return err.operation + ": " + err.causes[0].Error()
}

func (err *classifiedError) Unwrap() []error {
	return err.causes
}
