// Package publish verifies and atomically publishes generated stategeodb MMDB
// artifacts to local macOS and Linux filesystems.
package publish

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	temporaryPrefix = ".stategeodb-publish-"
	nameAttempts    = 8
)

var (
	// ErrInvalidRequest classifies missing or unsafe publication paths.
	ErrInvalidRequest = errors.New("publish: invalid request")
	// ErrUnsupportedPlatform classifies operating systems without this package's
	// atomic local replacement guarantee.
	ErrUnsupportedPlatform = errors.New("publish: unsupported platform")
	// ErrCandidate classifies candidate path, identity, or file failures.
	ErrCandidate = errors.New("publish: invalid candidate")
	// ErrDestination classifies destination parent, identity, or file failures.
	ErrDestination = errors.New("publish: invalid destination")
	// ErrCompare classifies exact comparison or identity-revalidation failures.
	ErrCompare = errors.New("publish: comparison failure")
	// ErrWrite classifies temporary creation, copy, mode, sync, or close failures.
	ErrWrite = errors.New("publish: temporary write failure")
	// ErrVerify classifies temporary artifact verification failures.
	ErrVerify = errors.New("publish: verification failure")
	// ErrReplace classifies failure at the atomic rename commit point.
	ErrReplace = errors.New("publish: replacement failure")
	// ErrCleanup classifies temporary-file or root cleanup failures.
	ErrCleanup = errors.New("publish: cleanup failure")
)

// Request names one caller-owned candidate and one explicit stable destination.
type Request struct {
	CandidatePath   string
	DestinationPath string
}

// Action describes the committed filesystem effect of a successful Publish.
type Action string

const (
	// ActionCreated means the destination did not exist and was installed.
	ActionCreated Action = "created"
	// ActionReplaced means a different regular destination was atomically replaced.
	ActionReplaced Action = "replaced"
	// ActionUnchanged means exact destination bytes already matched the candidate.
	ActionUnchanged Action = "unchanged"
)

// Result is the path-free publication result for the verified candidate bytes.
type Result struct {
	Action Action
	Size   int64
	SHA256 [sha256.Size]byte
}

// Publish copies one identity-checked candidate to a verified temporary sibling
// and atomically commits it on macOS or Linux. Callers must prevent concurrent
// mutation and serialize publication per destination. The candidate is never
// modified or deleted. Cancellation after the rename commit point cannot turn
// the completed publication into failure.
func Publish(ctx context.Context, request Request) (Result, error) {
	return publish(ctx, request, defaultOperations())
}

func publish(ctx context.Context, request Request, operations operations) (result Result, err error) {
	paths, err := validateRequest(ctx, request, operations.goos)
	if err != nil {
		return Result{}, err
	}

	session := publicationSession{}
	defer func() {
		err = session.finish(err)
		if err != nil && !session.isCommitted {
			result = Result{}
		}
	}()

	session.candidateRoot, err = operations.openRoot(paths.candidateParent)
	if err != nil {
		return Result{}, classified("open candidate parent", ErrCandidate)
	}
	if _, err := bindRootPath(
		session.candidateRoot,
		paths.candidateParent,
		operations.statPath,
		ErrCandidate,
	); err != nil {
		return Result{}, err
	}
	candidate, candidateInfo, err := openBoundRegular(
		session.candidateRoot,
		paths.candidateName,
		ErrCandidate,
	)
	if err != nil {
		return Result{}, err
	}
	if candidateInfo.Size() <= 0 {
		return Result{}, closeFile(candidate, classified("validate candidate size", ErrCandidate))
	}

	session.destinationRoot, err = operations.openRoot(paths.destinationParent)
	if err != nil {
		return Result{}, closeFile(candidate, classified("open destination parent", ErrDestination))
	}
	destinationRootInfo, err := bindRootPath(
		session.destinationRoot,
		paths.destinationParent,
		operations.statPath,
		ErrDestination,
	)
	if err != nil {
		return Result{}, closeFile(candidate, err)
	}
	action, destinationInfo, err := inspectDestination(
		session.destinationRoot,
		paths.destinationName,
	)
	if err != nil {
		return Result{}, closeFile(candidate, err)
	}

	temporary, temporaryName, temporaryInfo, err := createTemporary(session.destinationRoot, operations)
	if err != nil {
		if temporary != nil {
			session.temporaryName = temporaryName
			session.temporaryInfo = temporaryInfo
			session.hasTemporary = true
			err = closeFileWithClass(temporary, err, ErrWrite)
		}
		return Result{}, closeFile(candidate, err)
	}
	session.temporaryName = temporaryName
	session.temporaryInfo = temporaryInfo
	session.hasTemporary = true

	copyResult, copyErr := operations.copy(ctx, temporary, candidate)
	if copyErr != nil && !isClassifiedCopyError(copyErr) {
		copyErr = classified("copy candidate", ErrWrite)
	}
	copyErr = closeFile(candidate, copyErr)
	if copyErr != nil {
		return Result{}, closeFileWithClass(temporary, copyErr, ErrWrite)
	}
	if copyResult.size <= 0 || copyResult.size != candidateInfo.Size() {
		return Result{}, closeFileWithClass(
			temporary,
			classified("validate copied size", ErrWrite),
			ErrWrite,
		)
	}
	if err := temporary.Sync(); err != nil {
		return Result{}, closeFileWithClass(
			temporary,
			classified("synchronize temporary artifact", ErrWrite),
			ErrWrite,
		)
	}
	if err := temporary.Close(); err != nil {
		return Result{}, classified("close temporary artifact", ErrWrite)
	}

	if err := verifyTemporary(
		ctx,
		session.destinationRoot,
		temporaryName,
		temporaryInfo,
		operations.verify,
	); err != nil {
		return Result{}, err
	}

	if action != ActionCreated {
		comparison, compareErr := compareDestination(
			ctx,
			session.destinationRoot,
			temporaryName,
			paths.destinationName,
			temporaryInfo,
			destinationInfo,
			operations.compare,
		)
		if compareErr != nil {
			return Result{}, compareErr
		}
		if comparison.isEqual {
			if comparison.destinationSHA256 != copyResult.sha256 {
				return Result{}, classified("validate equal digest", ErrCompare)
			}
			if err := validateRootPath(
				paths.destinationParent,
				destinationRootInfo,
				operations.statPath,
			); err != nil {
				return Result{}, err
			}
			if err := removeTemporary(session.destinationRoot, temporaryName, temporaryInfo); err != nil {
				return Result{}, err
			}
			session.hasTemporary = false
			return Result{
				Action: ActionUnchanged,
				Size:   copyResult.size,
				SHA256: copyResult.sha256,
			}, nil
		}
		action = ActionReplaced
	}

	if err := operations.beforeRename(ctx); err != nil {
		if !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded) {
			err = classified("prepare replacement", ErrReplace)
		}
		return Result{}, err
	}
	if err := validateCommitDestination(
		session.destinationRoot,
		paths.destinationName,
		action,
		destinationInfo,
	); err != nil {
		return Result{}, err
	}
	if err := validateRootPath(
		paths.destinationParent,
		destinationRootInfo,
		operations.statPath,
	); err != nil {
		return Result{}, err
	}
	if err := validateTemporaryIdentity(
		session.destinationRoot,
		temporaryName,
		temporaryInfo,
		ErrCompare,
	); err != nil {
		return Result{}, err
	}
	if err := contextError(ctx); err != nil {
		return Result{}, err
	}
	if err := session.destinationRoot.Rename(temporaryName, paths.destinationName); err != nil {
		return Result{}, classified("replace destination", ErrReplace)
	}
	session.hasTemporary = false
	session.isCommitted = true
	return Result{
		Action: action,
		Size:   copyResult.size,
		SHA256: copyResult.sha256,
	}, nil
}

type publicationPaths struct {
	candidateParent   string
	candidateName     string
	destinationParent string
	destinationName   string
}

func validateRequest(ctx context.Context, request Request, goos string) (publicationPaths, error) {
	if ctx == nil {
		return publicationPaths{}, classified("validate context", ErrInvalidRequest)
	}
	if err := ctx.Err(); err != nil {
		return publicationPaths{}, err
	}
	if goos != "darwin" && goos != "linux" {
		return publicationPaths{}, classified("validate platform", ErrUnsupportedPlatform)
	}
	if request.CandidatePath == "" || request.DestinationPath == "" {
		return publicationPaths{}, classified("validate paths", ErrInvalidRequest)
	}

	candidateParent, candidateName, ok := splitPath(request.CandidatePath)
	if !ok {
		return publicationPaths{}, classified("validate candidate path", ErrInvalidRequest)
	}
	destinationParent, destinationName, ok := splitPath(request.DestinationPath)
	if !ok {
		return publicationPaths{}, classified("validate destination path", ErrInvalidRequest)
	}
	return publicationPaths{
		candidateParent:   candidateParent,
		candidateName:     candidateName,
		destinationParent: destinationParent,
		destinationName:   destinationName,
	}, nil
}

func splitPath(path string) (string, string, bool) {
	parent, name := filepath.Split(path)
	if name == "" || name == "." || name == ".." {
		return "", "", false
	}
	if parent == "" {
		parent = "."
	}
	return parent, name, true
}

func classified(operation string, causes ...error) error {
	return &classifiedError{operation: operation, causes: causes}
}

type classifiedError struct {
	operation string
	causes    []error
}

func (err *classifiedError) Error() string {
	return fmt.Sprintf("publish %s: %v", err.operation, err.causes[0])
}

func (err *classifiedError) Unwrap() []error {
	return err.causes
}

type publicationSession struct {
	candidateRoot   root
	destinationRoot root
	temporaryName   string
	temporaryInfo   os.FileInfo
	hasTemporary    bool
	isCommitted     bool
}

func (session *publicationSession) finish(primary error) error {
	if session.hasTemporary && session.destinationRoot != nil {
		if session.temporaryInfo == nil {
			primary = errors.Join(
				primary,
				classified("identify temporary artifact for cleanup", ErrCleanup),
			)
		} else if err := removeTemporary(
			session.destinationRoot,
			session.temporaryName,
			session.temporaryInfo,
		); err != nil {
			primary = errors.Join(primary, err)
		}
	}
	if session.destinationRoot != nil {
		if err := session.destinationRoot.Close(); err != nil {
			primary = errors.Join(primary, classified("close destination root", ErrCleanup))
		}
	}
	if session.candidateRoot != nil {
		if err := session.candidateRoot.Close(); err != nil {
			primary = errors.Join(primary, classified("close candidate root", ErrCleanup))
		}
	}
	return primary
}

func closeFile(file file, primary error) error {
	if err := file.Close(); err != nil {
		primary = errors.Join(primary, classified("close file", ErrCandidate))
	}
	return primary
}

func closeFileWithClass(file file, primary error, class error) error {
	if err := file.Close(); err != nil {
		primary = errors.Join(primary, classified("close file", class))
	}
	return primary
}

func isClassifiedCopyError(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, ErrCandidate) ||
		errors.Is(err, ErrWrite)
}

func defaultOperations() operations {
	return newOperations(runtime.GOOS)
}
