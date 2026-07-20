package publish

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"os"
)

const streamBufferSize = 64 * 1024

type copyResult struct {
	size   int64
	sha256 [sha256.Size]byte
}

func copyAndHash(ctx context.Context, destination io.Writer, candidate io.Reader) (copyResult, error) {
	digest := sha256.New()
	buffer := make([]byte, streamBufferSize)
	var size int64
	for {
		if err := contextError(ctx); err != nil {
			return copyResult{}, err
		}
		read, readErr := candidate.Read(buffer)
		if read > 0 {
			written, writeErr := destination.Write(buffer[:read])
			if written > 0 {
				hashed, hashErr := digest.Write(buffer[:written])
				if hashErr != nil || hashed != written {
					return copyResult{}, classified("hash candidate", ErrWrite)
				}
				size += int64(written)
			}
			if writeErr != nil {
				return copyResult{}, classified("copy candidate", ErrWrite)
			}
			if written != read {
				return copyResult{}, classified("copy candidate", ErrWrite)
			}
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				break
			}
			return copyResult{}, classified("read candidate", ErrCandidate)
		}
		if read == 0 {
			return copyResult{}, classified("read candidate progress", ErrCandidate)
		}
	}
	if err := contextError(ctx); err != nil {
		return copyResult{}, err
	}
	result := copyResult{size: size}
	copy(result.sha256[:], digest.Sum(nil))
	return result, nil
}

type comparison struct {
	isEqual           bool
	destinationSHA256 [sha256.Size]byte
}

func compareDestination(
	ctx context.Context,
	openedRoot root,
	temporaryName string,
	destinationName string,
	expectedTemporaryInfo os.FileInfo,
	destinationInfo os.FileInfo,
	compare func(context.Context, io.Reader, io.Reader) (comparison, error),
) (comparison, error) {
	temporary, temporaryInfo, err := openBoundRegular(openedRoot, temporaryName, ErrCompare)
	if err != nil {
		return comparison{}, err
	}
	if !os.SameFile(expectedTemporaryInfo, temporaryInfo) {
		return comparison{}, closeFileWithClass(
			temporary,
			classified("bind compared temporary identity", ErrCompare),
			ErrCompare,
		)
	}
	destination, openedDestinationInfo, err := openBoundRegular(
		openedRoot,
		destinationName,
		ErrDestination,
	)
	if err != nil {
		return comparison{}, closeFileWithClass(temporary, err, ErrCompare)
	}
	if !os.SameFile(destinationInfo, openedDestinationInfo) {
		primary := classified("bind destination identity", ErrCompare)
		primary = closeFileWithClass(destination, primary, ErrCompare)
		return comparison{}, closeFileWithClass(temporary, primary, ErrCompare)
	}

	result, primary := compare(ctx, temporary, destination)
	if primary != nil && !errors.Is(primary, context.Canceled) &&
		!errors.Is(primary, context.DeadlineExceeded) &&
		!errors.Is(primary, ErrCompare) {
		primary = classified("compare artifacts", ErrCompare)
	}
	primary = closeFileWithClass(destination, primary, ErrCompare)
	primary = closeFileWithClass(temporary, primary, ErrCompare)
	if primary != nil {
		return comparison{}, primary
	}
	result.isEqual = result.isEqual && temporaryInfo.Size() == openedDestinationInfo.Size()
	currentDestination, err := openedRoot.Lstat(destinationName)
	if err != nil || !currentDestination.Mode().IsRegular() ||
		!os.SameFile(openedDestinationInfo, currentDestination) {
		return comparison{}, classified("revalidate compared destination", ErrCompare)
	}
	currentTemporary, err := openedRoot.Lstat(temporaryName)
	if err != nil || !currentTemporary.Mode().IsRegular() ||
		!os.SameFile(expectedTemporaryInfo, currentTemporary) {
		return comparison{}, classified("revalidate compared temporary", ErrCompare)
	}
	return result, nil
}

func compareStreams(
	ctx context.Context,
	candidate io.Reader,
	destination io.Reader,
) (comparison, error) {
	candidateBuffer := make([]byte, streamBufferSize)
	destinationBuffer := make([]byte, streamBufferSize)
	destinationDigest := sha256.New()
	isEqual := true
	candidateDone := false
	destinationDone := false
	for {
		if err := contextError(ctx); err != nil {
			return comparison{}, err
		}
		candidateRead, candidateErr := readComparisonChunk(ctx, candidate, candidateBuffer, candidateDone)
		destinationRead, destinationErr := readComparisonChunk(ctx, destination, destinationBuffer, destinationDone)
		candidateDone = candidateDone || errors.Is(candidateErr, io.EOF)
		destinationDone = destinationDone || errors.Is(destinationErr, io.EOF)
		if destinationRead > 0 {
			hashed, hashErr := destinationDigest.Write(destinationBuffer[:destinationRead])
			if hashErr != nil || hashed != destinationRead {
				return comparison{}, classified("hash destination", ErrCompare)
			}
		}
		if candidateRead != destinationRead ||
			!bytes.Equal(candidateBuffer[:candidateRead], destinationBuffer[:destinationRead]) {
			isEqual = false
		}
		if errors.Is(candidateErr, context.Canceled) || errors.Is(candidateErr, context.DeadlineExceeded) {
			return comparison{}, candidateErr
		}
		if errors.Is(destinationErr, context.Canceled) || errors.Is(destinationErr, context.DeadlineExceeded) {
			return comparison{}, destinationErr
		}
		if candidateErr != nil && !errors.Is(candidateErr, io.EOF) {
			return comparison{}, classified("read temporary artifact", ErrCompare)
		}
		if destinationErr != nil && !errors.Is(destinationErr, io.EOF) {
			return comparison{}, classified("read destination", ErrCompare)
		}
		if candidateDone && destinationDone {
			break
		}
	}
	if err := contextError(ctx); err != nil {
		return comparison{}, err
	}
	result := comparison{isEqual: isEqual}
	copy(result.destinationSHA256[:], destinationDigest.Sum(nil))
	return result, nil
}

func readComparisonChunk(
	ctx context.Context,
	reader io.Reader,
	buffer []byte,
	done bool,
) (int, error) {
	if done {
		return 0, io.EOF
	}
	total := 0
	for total < len(buffer) {
		if err := contextError(ctx); err != nil {
			return total, err
		}
		read, err := reader.Read(buffer[total:])
		total += read
		if err != nil {
			return total, err
		}
		if read == 0 {
			return total, io.ErrNoProgress
		}
	}
	return total, nil
}
