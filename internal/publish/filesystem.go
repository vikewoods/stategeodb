package publish

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"strconv"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/vikewoods/stategeodb/internal/artifact"
)

type file interface {
	io.Reader
	io.Writer
	Chmod(os.FileMode) error
	Close() error
	Fd() uintptr
	Stat() (os.FileInfo, error)
	Sync() error
}

type root interface {
	Close() error
	Info() (os.FileInfo, error)
	Lstat(string) (os.FileInfo, error)
	Open(string) (file, error)
	OpenFile(string, int, os.FileMode) (file, error)
	Remove(string) error
	Rename(string, string) error
}

type operations struct {
	goos         string
	openRoot     func(string) (root, error)
	statPath     func(string) (os.FileInfo, error)
	randomName   func() (string, error)
	copy         func(context.Context, io.Writer, io.Reader) (copyResult, error)
	verify       func(context.Context, file) error
	compare      func(context.Context, io.Reader, io.Reader) (comparison, error)
	beforeRename func(context.Context) error
}

type osRoot struct {
	root *os.Root
}

func newOperations(goos string) operations {
	return operations{
		goos: goos,
		openRoot: func(path string) (root, error) {
			opened, err := os.OpenRoot(path)
			if err != nil {
				return nil, err
			}
			return osRoot{root: opened}, nil
		},
		statPath:   os.Stat,
		randomName: randomTemporaryName,
		copy:       copyAndHash,
		verify: func(ctx context.Context, opened file) error {
			return verifyDescriptor(ctx, opened, goos)
		},
		compare:      compareStreams,
		beforeRename: contextError,
	}
}

func (root osRoot) Close() error {
	return root.root.Close()
}

func (root osRoot) Info() (os.FileInfo, error) {
	opened, err := root.root.Open(".")
	if err != nil {
		return nil, err
	}
	info, statErr := opened.Stat()
	if closeErr := opened.Close(); closeErr != nil {
		statErr = errors.Join(statErr, closeErr)
	}
	return info, statErr
}

func (root osRoot) Lstat(name string) (os.FileInfo, error) {
	return root.root.Lstat(name)
}

func (root osRoot) Open(name string) (file, error) {
	return root.root.Open(name)
}

func (root osRoot) OpenFile(name string, flag int, mode os.FileMode) (file, error) {
	return root.root.OpenFile(name, flag, mode)
}

func (root osRoot) Remove(name string) error {
	return root.root.Remove(name)
}

func (root osRoot) Rename(oldName string, newName string) error {
	return root.root.Rename(oldName, newName)
}

func openBoundRegular(openedRoot root, name string, class error) (file, os.FileInfo, error) {
	listed, err := openedRoot.Lstat(name)
	if err != nil || !listed.Mode().IsRegular() || listed.Mode()&os.ModeSymlink != 0 {
		return nil, nil, classified("inspect regular file", class)
	}
	opened, err := openedRoot.Open(name)
	if err != nil {
		return nil, nil, classified("open regular file", class)
	}
	info, err := opened.Stat()
	if err != nil || !info.Mode().IsRegular() || !os.SameFile(listed, info) {
		return nil, nil, closeFileWithClass(
			opened,
			classified("bind regular file identity", class),
			class,
		)
	}
	current, err := openedRoot.Lstat(name)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) {
		return nil, nil, closeFileWithClass(
			opened,
			classified("revalidate regular file identity", class),
			class,
		)
	}
	return opened, info, nil
}

func bindRootPath(
	openedRoot root,
	path string,
	statPath func(string) (os.FileInfo, error),
	class error,
) (os.FileInfo, error) {
	rootInfo, err := openedRoot.Info()
	if err != nil || !rootInfo.IsDir() {
		return nil, classified("inspect opened parent", class)
	}
	pathInfo, err := statPath(path)
	if err != nil || !pathInfo.IsDir() || !os.SameFile(rootInfo, pathInfo) {
		return nil, classified("bind parent identity", class)
	}
	return rootInfo, nil
}

func validateRootPath(
	path string,
	expected os.FileInfo,
	statPath func(string) (os.FileInfo, error),
) error {
	current, err := statPath(path)
	if err != nil || !current.IsDir() || !os.SameFile(expected, current) {
		return classified("revalidate destination parent identity", ErrCompare)
	}
	return nil
}

func createTemporary(openedRoot root, operations operations) (file, string, os.FileInfo, error) {
	for range nameAttempts {
		name, err := operations.randomName()
		if err != nil {
			return nil, "", nil, classified("generate temporary name", ErrWrite)
		}
		opened, err := openedRoot.OpenFile(
			name,
			os.O_WRONLY|os.O_CREATE|os.O_EXCL,
			0o644,
		)
		if errors.Is(err, os.ErrExist) {
			continue
		}
		if err != nil {
			return nil, "", nil, classified("create temporary artifact", ErrWrite)
		}
		info, err := opened.Stat()
		if err != nil || !info.Mode().IsRegular() {
			return opened, name, nil, classified("inspect temporary artifact", ErrWrite)
		}
		if err := opened.Chmod(0o644); err != nil {
			return opened, name, info, classified("set temporary mode", ErrWrite)
		}
		current, err := openedRoot.Lstat(name)
		if err != nil || !current.Mode().IsRegular() || !os.SameFile(info, current) {
			return opened, name, info, classified("bind temporary identity", ErrWrite)
		}
		return opened, name, info, nil
	}
	return nil, "", nil, classified("exhaust temporary names", ErrWrite)
}

func randomTemporaryName() (string, error) {
	var token [16]byte
	if _, err := rand.Read(token[:]); err != nil {
		return "", err
	}
	return temporaryPrefix + hex.EncodeToString(token[:]), nil
}

func verifyTemporary(
	ctx context.Context,
	openedRoot root,
	name string,
	expected os.FileInfo,
	verify func(context.Context, file) error,
) error {
	opened, info, err := openBoundRegular(openedRoot, name, ErrVerify)
	if err != nil {
		return err
	}
	if !os.SameFile(expected, info) {
		return closeFileWithClass(
			opened,
			classified("bind verified temporary identity", ErrVerify),
			ErrVerify,
		)
	}
	primary := verify(ctx, opened)
	if primary != nil && !errors.Is(primary, context.Canceled) &&
		!errors.Is(primary, context.DeadlineExceeded) {
		primary = classified("verify temporary artifact", ErrVerify, primary)
	}
	if current, lstatErr := openedRoot.Lstat(name); lstatErr != nil ||
		!current.Mode().IsRegular() || !os.SameFile(expected, current) {
		primary = errors.Join(primary, classified("revalidate verified artifact", ErrVerify))
	}
	return closeFileWithClass(opened, primary, ErrVerify)
}

func validateTemporaryIdentity(
	openedRoot root,
	name string,
	expected os.FileInfo,
	class error,
) error {
	current, err := openedRoot.Lstat(name)
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(expected, current) {
		return classified("revalidate temporary identity", class)
	}
	return nil
}

func removeTemporary(
	openedRoot root,
	name string,
	expected os.FileInfo,
) error {
	if err := validateTemporaryIdentity(openedRoot, name, expected, ErrCleanup); err != nil {
		return err
	}
	if err := openedRoot.Remove(name); err != nil {
		return classified("remove temporary artifact", ErrCleanup)
	}
	return nil
}

func verifyDescriptor(ctx context.Context, opened file, goos string) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	descriptorPath := "/dev/fd/" + strconv.FormatUint(uint64(opened.Fd()), 10)
	if goos == "linux" {
		descriptorPath = "/proc/self/fd/" + strconv.FormatUint(uint64(opened.Fd()), 10)
	}
	reader, err := maxminddb.Open(descriptorPath)
	if err != nil {
		return artifact.ErrCorrupt
	}
	primary := artifact.Verify(ctx, reader)
	if closeErr := reader.Close(); closeErr != nil {
		primary = errors.Join(primary, artifact.ErrCorrupt)
	}
	return primary
}

func inspectDestination(
	openedRoot root,
	name string,
) (Action, os.FileInfo, error) {
	info, err := openedRoot.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return ActionCreated, nil, nil
	}
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", nil, classified("inspect destination", ErrDestination)
	}
	return ActionReplaced, info, nil
}

func validateCommitDestination(
	openedRoot root,
	name string,
	action Action,
	expected os.FileInfo,
) error {
	current, err := openedRoot.Lstat(name)
	if action == ActionCreated {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return classified("revalidate absent destination", ErrCompare)
	}
	if err != nil || !current.Mode().IsRegular() || !os.SameFile(expected, current) {
		return classified("revalidate destination identity", ErrCompare)
	}
	return nil
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return classified("validate context", ErrInvalidRequest)
	}
	return ctx.Err()
}
