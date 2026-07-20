// Package mmdb encodes compliance-profile records as minimal MaxMind DB files.
package mmdb

import (
	"errors"
	"io"
	"net"
	"net/netip"
	"reflect"
	"slices"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"

	"github.com/vikewoods/stategeodb/internal/artifactprofile"
	"github.com/vikewoods/stategeodb/internal/source"
)

const (
	// SchemaVersion is the machine-readable revision of SchemaDescription.
	SchemaVersion = 1
	// RecordSize is the search-tree record size used for newly generated artifacts.
	RecordSize = 24
	// DatabaseType identifies the runtime record shape. Incompatible runtime
	// record changes require a new database type.
	DatabaseType = "StateGeo-Country-USSubdivision"
	// SchemaDescription identifies compatible revisions of DatabaseType.
	// Compatible schema changes retain DatabaseType and increment this version.
	SchemaDescription = "stategeodb country with US subdivision schema v1"
)

var (
	// ErrInvalidInput classifies invalid writer configuration or records.
	ErrInvalidInput = errors.New("mmdb: invalid input")
	// ErrBuild classifies MMDB tree construction or record insertion failures.
	ErrBuild = errors.New("mmdb: build failure")
	// ErrWrite classifies failures writing the completed MMDB to its destination.
	ErrWrite = errors.New("mmdb: destination write failure")
)

// Options contains the caller-controlled deterministic MMDB settings.
type Options struct {
	BuildEpoch int64
}

// Write validates and deterministically encodes records to destination. It
// returns the byte count reported by the upstream writer and never closes the
// destination. On failure, Write returns a zero byte count.
func Write(destination io.Writer, records []source.Record, options Options) (int64, error) {
	if isNilWriter(destination) {
		return 0, classified("validate destination", ErrInvalidInput)
	}
	if options.BuildEpoch <= 0 {
		return 0, classified("validate build epoch", ErrInvalidInput)
	}
	if len(records) == 0 {
		return 0, classified("validate records", ErrInvalidInput)
	}

	sortedRecords := slices.Clone(records)
	ipv4Storage := netip.MustParsePrefix("::/96")
	needsIPv4Restore := false
	for _, record := range sortedRecords {
		if err := artifactprofile.Validate(record); err != nil {
			return 0, invalidRecordError{cause: err}
		}
		if record.Prefix.Addr().Is6() && record.Prefix.Bits() >= ipv4Storage.Bits() &&
			ipv4Storage.Contains(record.Prefix.Addr()) {
			return 0, classified("validate address family boundary", ErrInvalidInput)
		}
		if record.Prefix.Addr().Is6() && record.Prefix.Bits() < ipv4Storage.Bits() &&
			record.Prefix.Contains(ipv4Storage.Addr()) {
			needsIPv4Restore = true
		}
	}

	slices.SortFunc(sortedRecords, source.Compare)
	for index := 1; index < len(sortedRecords); index++ {
		if sortedRecords[index-1].Prefix == sortedRecords[index].Prefix {
			return 0, classified("validate unique prefixes", ErrInvalidInput)
		}
	}

	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch:              options.BuildEpoch,
		DatabaseType:            DatabaseType,
		Description:             map[string]string{"en": SchemaDescription},
		DisableIPv4Aliasing:     false,
		IncludeReservedNetworks: true,
		IPVersion:               6,
		Languages:               []string{},
		RecordSize:              RecordSize,
		DisableMetadataPointers: false,
	})
	if err != nil {
		return 0, classified("create writer", ErrBuild)
	}

	for _, record := range sortedRecords {
		if err := insertRecord(tree, record); err != nil {
			return 0, classified("insert record", ErrBuild)
		}
	}
	if needsIPv4Restore {
		remove := func(mmdbtype.DataType) (mmdbtype.DataType, error) {
			return nil, nil
		}
		if err := tree.InsertFunc(prefixNetwork(ipv4Storage), remove); err != nil {
			return 0, classified("restore ipv4 boundary", ErrBuild)
		}
		for _, record := range sortedRecords {
			if !record.Prefix.Addr().Is4() {
				continue
			}
			if err := insertRecord(tree, record); err != nil {
				return 0, classified("restore ipv4 record", ErrBuild)
			}
		}
	}

	written, err := tree.WriteTo(destination)
	if err != nil {
		return 0, classified("write database", ErrWrite)
	}
	return written, nil
}

func insertRecord(tree *mmdbwriter.Tree, record source.Record) error {
	encoded := encodeRecord(record)
	replace := func(mmdbtype.DataType) (mmdbtype.DataType, error) {
		return encoded, nil
	}
	return tree.InsertFunc(prefixNetwork(record.Prefix), replace)
}

func prefixNetwork(prefix netip.Prefix) *net.IPNet {
	if prefix.Addr().Is4() {
		address := prefix.Addr().As4()
		return &net.IPNet{
			IP:   net.IP(address[:]),
			Mask: net.CIDRMask(prefix.Bits(), 32),
		}
	}

	address := prefix.Addr().As16()
	return &net.IPNet{
		IP:   net.IP(address[:]),
		Mask: net.CIDRMask(prefix.Bits(), 128),
	}
}

func encodeRecord(record source.Record) mmdbtype.Map {
	if record.Country == "" {
		return mmdbtype.Map{}
	}

	encoded := mmdbtype.Map{
		"country": mmdbtype.Map{
			"iso_code": mmdbtype.String(record.Country),
		},
	}
	if record.Subdivision != "" {
		encoded["subdivisions"] = mmdbtype.Slice{
			mmdbtype.Map{
				"iso_code": mmdbtype.String(record.Subdivision),
			},
		}
	}
	return encoded
}

func isNilWriter(destination io.Writer) bool {
	if destination == nil {
		return true
	}

	value := reflect.ValueOf(destination)
	switch value.Kind() {
	case reflect.Chan, reflect.Func, reflect.Interface, reflect.Map, reflect.Pointer, reflect.Slice:
		return value.IsNil()
	default:
		return false
	}
}

func classified(operation string, classification error) error {
	return &classifiedError{
		operation:      operation,
		classification: classification,
	}
}

type classifiedError struct {
	operation      string
	classification error
}

func (err *classifiedError) Error() string {
	return err.operation + ": " + err.classification.Error()
}

func (err *classifiedError) Unwrap() error {
	return err.classification
}

type invalidRecordError struct {
	cause error
}

func (err invalidRecordError) Error() string {
	return "validate record: " + ErrInvalidInput.Error()
}

func (err invalidRecordError) Unwrap() []error {
	return []error{ErrInvalidInput, err.cause}
}
