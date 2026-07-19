// Package maxmind opens and verifies supported MaxMind City databases.
//
// This package owns the lifetime of the upstream reader. It intentionally does
// not expose lookup, traversal, or record-decoding operations yet.
package maxmind

import (
	"errors"
	"io"
	"maps"
	"os"
	"slices"
	"unicode/utf8"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"
)

// Errors returned by this package.
var (
	ErrOpen        = errors.New("maxmind: source database open or read failure")
	ErrCorrupt     = errors.New("maxmind: corrupt source database")
	ErrUnsupported = errors.New("maxmind: unsupported source database")
	ErrClose       = errors.New("maxmind: source database close failure")
)

// Metadata is an application-owned snapshot of source database metadata.
// Description and Languages are copied each time Metadata is returned.
type Metadata struct {
	// DatabaseType identifies the provider record schema.
	DatabaseType string
	// BinaryFormatMajorVersion is the MMDB format major version.
	BinaryFormatMajorVersion uint
	// BinaryFormatMinorVersion is the MMDB format minor version.
	BinaryFormatMinorVersion uint
	// BuildEpoch is the database build time in Unix seconds.
	BuildEpoch uint
	// IPVersion is 4 for IPv4-only databases and 6 for dual-stack databases.
	IPVersion uint
	// NodeCount is the number of search-tree nodes.
	NodeCount uint
	// RecordSize is the search-tree record size in bits.
	RecordSize uint
	// Languages lists locales that may appear in database records.
	Languages []string
	// Description maps locale codes to localized database descriptions.
	Description map[string]string
}

// Database owns one verified upstream reader. Database values must not be
// copied after use; Open returns a pointer for that reason.
//
// Close must not run concurrently with itself or with future reader operations.
// Metadata is an independent snapshot and remains available after Close.
type Database struct {
	noCopy noCopy

	reader      *maxminddb.Reader
	metadata    Metadata
	closeReader func(*maxminddb.Reader) error
}

type noCopy struct{}

func (*noCopy) Lock()   {}
func (*noCopy) Unlock() {}

type readerOperations struct {
	open   func(string, ...maxminddb.ReaderOption) (*maxminddb.Reader, error)
	verify func(*maxminddb.Reader) error
	close  func(*maxminddb.Reader) error
}

// Open opens path with the pinned MaxMind reader, validates compatible City
// metadata, and runs full structural verification before returning ownership.
// The caller must close a successful result.
func Open(path string) (*Database, error) {
	return openWithOperations(path, readerOperations{
		open:   maxminddb.Open,
		verify: (*maxminddb.Reader).Verify,
		close:  (*maxminddb.Reader).Close,
	})
}

func openWithOperations(path string, operations readerOperations) (*Database, error) {
	reader, err := operations.open(path)
	if err != nil {
		return nil, classifyOpenError(err)
	}

	metadata := metadataViewFromUpstream(reader.Metadata)
	if err := validateMetadata(metadata); err != nil {
		return nil, closeAfterFailure(reader, err, operations.close)
	}

	if err := operations.verify(reader); err != nil {
		primary := newClassifiedError("verify source database", ErrCorrupt)
		return nil, closeAfterFailure(reader, primary, operations.close)
	}

	return &Database{
		reader:      reader,
		metadata:    metadata.clone(),
		closeReader: operations.close,
	}, nil
}

// Metadata returns a defensive copy of the metadata captured during Open. It
// does not access the upstream reader and is safe to call after Close.
func (d *Database) Metadata() Metadata {
	if d == nil {
		return Metadata{}
	}

	return d.metadata.clone()
}

// Close releases the upstream reader. Repeated calls after the first are
// harmless. Close must not be called concurrently with other Database methods.
func (d *Database) Close() error {
	if d == nil || d.reader == nil {
		return nil
	}

	reader := d.reader
	d.reader = nil

	closeReader := d.closeReader
	d.closeReader = nil
	if closeReader == nil {
		closeReader = (*maxminddb.Reader).Close
	}

	if err := closeReader(reader); err != nil {
		return newClassifiedError("close source database", ErrClose)
	}
	return nil
}

func metadataViewFromUpstream(metadata maxminddb.Metadata) Metadata {
	return Metadata{
		DatabaseType:             metadata.DatabaseType,
		BinaryFormatMajorVersion: metadata.BinaryFormatMajorVersion,
		BinaryFormatMinorVersion: metadata.BinaryFormatMinorVersion,
		BuildEpoch:               metadata.BuildEpoch,
		IPVersion:                metadata.IPVersion,
		NodeCount:                metadata.NodeCount,
		RecordSize:               metadata.RecordSize,
		Languages:                metadata.Languages,
		Description:              metadata.Description,
	}
}

func (m Metadata) clone() Metadata {
	m.Languages = slices.Clone(m.Languages)
	m.Description = maps.Clone(m.Description)
	return m
}

func validateMetadata(metadata Metadata) error {
	if !metadataStringsValid(metadata) {
		return unsupportedMetadata("invalid metadata text")
	}

	switch metadata.DatabaseType {
	case "GeoLite2-City", "GeoIP2-City":
	default:
		return unsupportedMetadata("unsupported database type")
	}

	if metadata.BinaryFormatMajorVersion != 2 {
		return unsupportedMetadata("unsupported binary format major version")
	}
	if metadata.BinaryFormatMinorVersion != 0 {
		return unsupportedMetadata("unsupported binary format minor version")
	}
	if metadata.IPVersion != 4 && metadata.IPVersion != 6 {
		return unsupportedMetadata("unsupported ip version")
	}
	if metadata.NodeCount == 0 {
		return unsupportedMetadata("invalid node count")
	}
	switch metadata.RecordSize {
	case 24, 28, 32:
	default:
		return unsupportedMetadata("unsupported record size")
	}
	if len(metadata.Description) == 0 {
		return unsupportedMetadata("empty descriptions")
	}

	return nil
}

func metadataStringsValid(metadata Metadata) bool {
	if !utf8.ValidString(metadata.DatabaseType) {
		return false
	}
	for _, language := range metadata.Languages {
		if !utf8.ValidString(language) {
			return false
		}
	}
	for language, description := range metadata.Description {
		if !utf8.ValidString(language) || !utf8.ValidString(description) {
			return false
		}
	}
	return true
}

func unsupportedMetadata(context string) error {
	return newClassifiedError(context, ErrUnsupported)
}

func classifyOpenError(err error) error {
	if isFilesystemError(err) {
		return newClassifiedError("open source database", ErrOpen)
	}
	return newClassifiedError("open source database", ErrCorrupt)
}

func isFilesystemError(err error) bool {
	var pathError *os.PathError
	if errors.As(err, &pathError) {
		return true
	}

	var syscallError *os.SyscallError
	return errors.As(err, &syscallError) || errors.Is(err, io.ErrUnexpectedEOF)
}

func closeAfterFailure(
	reader *maxminddb.Reader,
	primary error,
	closeReader func(*maxminddb.Reader) error,
) error {
	if err := closeReader(reader); err != nil {
		closeError := newClassifiedError("close source database", ErrClose)
		return errors.Join(primary, closeError)
	}
	return primary
}

type classifiedError struct {
	context string
	class   error
}

func newClassifiedError(context string, class error) error {
	return &classifiedError{
		context: context,
		class:   class,
	}
}

func (e *classifiedError) Error() string {
	return e.context + ": " + e.class.Error()
}

func (e *classifiedError) Unwrap() error {
	return e.class
}
