package cli

import (
	"context"
	"errors"
	"flag"
	"io"
	"net/netip"
	"strconv"

	"github.com/vikewoods/stategeodb/internal/inspect"
	"github.com/vikewoods/stategeodb/internal/mmdb"
	"github.com/vikewoods/stategeodb/internal/source"
)

const (
	maxInspectLookups = 32

	invalidInspectUsageText = "stategeodb: invalid inspect usage; run 'stategeodb inspect --help' for usage\n"
	inspectCancelledText    = "stategeodb: inspection cancelled\n"
	inspectOpenFailureText  = "stategeodb: database could not be opened\n"
	inspectUnsupportedText  = "stategeodb: database is unsupported\n"
	inspectCorruptText      = "stategeodb: database is corrupt\n"
	inspectLookupText       = "stategeodb: database lookup failed\n"
	inspectCloseText        = "stategeodb: database close failed\n"
	inspectOutputText       = "stategeodb: failed to write inspect output\n"
	inspectFailureText      = "stategeodb: inspection failed\n"
)

type inspectOperations struct {
	execute func(context.Context, inspect.Request) (inspect.Result, error)
}

func defaultInspectOperations() inspectOperations {
	return inspectOperations{execute: inspect.Inspect}
}

func runInspect(
	ctx context.Context,
	args []string,
	stdout io.Writer,
	stderr io.Writer,
	operations inspectOperations,
) int {
	request, ok := parseInspectArguments(args)
	if !ok {
		writeDiagnostic(stderr, invalidInspectUsageText)
		return exitFailure
	}

	result, err := operations.execute(ctx, request)
	if err != nil {
		writeDiagnostic(stderr, inspectDiagnostic(err))
		return exitFailure
	}
	output, ok := formatInspectOutput(result)
	if !ok || !writeString(stdout, output) {
		writeDiagnostic(stderr, inspectOutputText)
		return exitFailure
	}
	return exitSuccess
}

type singleStringValue struct {
	count int
	value string
}

func (value *singleStringValue) String() string {
	return value.value
}

func (value *singleStringValue) Set(input string) error {
	value.count++
	value.value = input
	return nil
}

type addressValues struct {
	values []string
}

func (values *addressValues) String() string {
	return ""
}

func (values *addressValues) Set(input string) error {
	if len(values.values) == maxInspectLookups {
		return errors.New("too many addresses")
	}
	values.values = append(values.values, input)
	return nil
}

func parseInspectArguments(args []string) (inspect.Request, bool) {
	for _, argument := range args {
		if argument == "--" {
			return inspect.Request{}, false
		}
	}

	flags := flag.NewFlagSet("inspect", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() {}
	var databasePath singleStringValue
	var addresses addressValues
	flags.Var(&databasePath, "database", "")
	flags.Var(&addresses, "ip", "")
	if err := flags.Parse(args); err != nil || flags.NArg() != 0 {
		return inspect.Request{}, false
	}
	if databasePath.count != 1 || databasePath.value == "" {
		return inspect.Request{}, false
	}

	parsedAddresses := make([]netip.Addr, len(addresses.values))
	for index, value := range addresses.values {
		if value == "" {
			return inspect.Request{}, false
		}
		address, err := netip.ParseAddr(value)
		if err != nil || address.Zone() != "" {
			return inspect.Request{}, false
		}
		parsedAddresses[index] = address
	}
	return inspect.Request{
		DatabasePath: databasePath.value,
		Addresses:    parsedAddresses,
	}, true
}

func formatInspectOutput(result inspect.Result) (string, bool) {
	if !validInspectResult(result) {
		return "", false
	}

	output := make([]byte, 0, 256+len(result.Lookups)*160)
	output = appendKeyValue(output, "database_type", result.Metadata.DatabaseType)
	output = appendInteger(output, "schema_version", uint64(result.Metadata.SchemaVersion))
	output = append(output, "build_epoch="...)
	output = strconv.AppendUint(output, uint64(result.Metadata.BuildEpoch), 10)
	output = append(output, '\n')
	output = append(output, "binary_format="...)
	output = strconv.AppendUint(output, uint64(result.Metadata.BinaryFormatMajor), 10)
	output = append(output, '.')
	output = strconv.AppendUint(output, uint64(result.Metadata.BinaryFormatMinor), 10)
	output = append(output, '\n')
	output = appendInteger(output, "ip_version", uint64(result.Metadata.IPVersion))
	output = appendInteger(output, "record_size", uint64(result.Metadata.RecordSize))
	output = appendInteger(output, "node_count", uint64(result.Metadata.NodeCount))
	output = appendInteger(output, "lookup_count", uint64(len(result.Lookups)))

	for index, lookup := range result.Lookups {
		prefix := "lookup_" + strconv.Itoa(index+1) + "_"
		output = appendKeyValue(output, prefix+"ip", lookup.Address.String())
		output = appendKeyValue(output, prefix+"found", strconv.FormatBool(lookup.Found))
		network := ""
		if lookup.Found {
			network = lookup.Prefix.String()
		}
		output = appendKeyValue(output, prefix+"network", network)
		output = appendKeyValue(output, prefix+"country", lookup.Country)
		output = appendKeyValue(output, prefix+"subdivision", lookup.Subdivision)
	}
	return string(output), true
}

func validInspectResult(result inspect.Result) bool {
	metadata := result.Metadata
	if metadata.DatabaseType != mmdb.DatabaseType ||
		metadata.SchemaVersion != mmdb.SchemaVersion ||
		metadata.BuildEpoch == 0 ||
		metadata.BinaryFormatMajor != 2 ||
		metadata.BinaryFormatMinor != 0 ||
		metadata.IPVersion != 6 ||
		metadata.RecordSize != mmdb.RecordSize ||
		metadata.NodeCount == 0 ||
		len(result.Lookups) > maxInspectLookups {
		return false
	}
	for _, lookup := range result.Lookups {
		if !lookup.Address.IsValid() || lookup.Address != lookup.Address.Unmap() {
			return false
		}
		country, subdivision, err := source.NormalizeLocation(lookup.Country, lookup.Subdivision)
		if err != nil || country != lookup.Country || subdivision != lookup.Subdivision {
			return false
		}
		if !lookup.Found {
			if lookup.Prefix.IsValid() || lookup.Country != "" || lookup.Subdivision != "" {
				return false
			}
			continue
		}
		prefix, err := source.NormalizePrefix(lookup.Prefix)
		if err != nil || prefix != lookup.Prefix || !prefix.Contains(lookup.Address) {
			return false
		}
	}
	return true
}

func appendKeyValue(destination []byte, key string, value string) []byte {
	destination = append(destination, key...)
	destination = append(destination, '=')
	destination = append(destination, value...)
	return append(destination, '\n')
}

func appendInteger(destination []byte, key string, value uint64) []byte {
	destination = append(destination, key...)
	destination = append(destination, '=')
	destination = strconv.AppendUint(destination, value, 10)
	return append(destination, '\n')
}

func inspectDiagnostic(err error) string {
	switch {
	case errors.Is(err, inspect.ErrInvalidRequest):
		return invalidInspectUsageText
	case errors.Is(err, inspect.ErrClose):
		return inspectCloseText
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return inspectCancelledText
	case errors.Is(err, inspect.ErrOpen):
		return inspectOpenFailureText
	case errors.Is(err, inspect.ErrUnsupported):
		return inspectUnsupportedText
	case errors.Is(err, inspect.ErrCorrupt):
		return inspectCorruptText
	case errors.Is(err, inspect.ErrLookup),
		errors.Is(err, source.ErrInvalidPrefix),
		errors.Is(err, source.ErrInvalidCountry),
		errors.Is(err, source.ErrInvalidSubdivision):
		return inspectLookupText
	default:
		return inspectFailureText
	}
}
