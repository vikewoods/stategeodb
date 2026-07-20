package artifact

import (
	"bytes"
	"net"
	"testing"

	"github.com/maxmind/mmdbwriter"
	"github.com/maxmind/mmdbwriter/mmdbtype"
	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/vikewoods/stategeodb/internal/mmdb"
)

const legacyRecordSize = 28

func TestCompatible_RequiresExactRuntimeMetadata(t *testing.T) {
	valid := maxminddb.Metadata{
		Description:              map[string]string{"en": mmdb.SchemaDescription},
		DatabaseType:             mmdb.DatabaseType,
		Languages:                []string{},
		BinaryFormatMajorVersion: 2,
		BinaryFormatMinorVersion: 0,
		BuildEpoch:               1,
		IPVersion:                6,
		NodeCount:                1,
		RecordSize:               mmdb.RecordSize,
	}
	if !Compatible(valid) {
		t.Fatal("Compatible(valid) = false")
	}

	tests := []struct {
		name   string
		mutate func(*maxminddb.Metadata)
	}{
		{name: "database type", mutate: func(metadata *maxminddb.Metadata) { metadata.DatabaseType = "other" }},
		{name: "description value", mutate: func(metadata *maxminddb.Metadata) { metadata.Description["en"] = "other" }},
		{name: "extra description", mutate: func(metadata *maxminddb.Metadata) { metadata.Description["fr"] = "other" }},
		{name: "languages", mutate: func(metadata *maxminddb.Metadata) { metadata.Languages = []string{"en"} }},
		{name: "major version", mutate: func(metadata *maxminddb.Metadata) { metadata.BinaryFormatMajorVersion = 1 }},
		{name: "minor version", mutate: func(metadata *maxminddb.Metadata) { metadata.BinaryFormatMinorVersion = 1 }},
		{name: "build epoch", mutate: func(metadata *maxminddb.Metadata) { metadata.BuildEpoch = 0 }},
		{name: "IP version", mutate: func(metadata *maxminddb.Metadata) { metadata.IPVersion = 4 }},
		{name: "node count", mutate: func(metadata *maxminddb.Metadata) { metadata.NodeCount = 0 }},
		{name: "legacy record size", mutate: func(metadata *maxminddb.Metadata) { metadata.RecordSize = legacyRecordSize }},
		{name: "32-bit record size", mutate: func(metadata *maxminddb.Metadata) { metadata.RecordSize = 32 }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			metadata := valid
			metadata.Description = map[string]string{"en": mmdb.SchemaDescription}
			test.mutate(&metadata)
			if Compatible(metadata) {
				t.Error("Compatible(mutated metadata) = true")
			}
		})
	}
}

func TestVerify_RequiresCurrentEncoding(t *testing.T) {
	for _, test := range []struct {
		name       string
		recordSize int
		expected   error
	}{
		{name: "current", recordSize: mmdb.RecordSize},
		{name: "legacy", recordSize: legacyRecordSize, expected: ErrUnsupported},
		{name: "32-bit", recordSize: 32, expected: ErrUnsupported},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := projectReader(t, test.recordSize)
			defer reader.Close()

			err := Verify(t.Context(), reader)
			if err != test.expected {
				t.Errorf("Verify() error = %v, want %v", err, test.expected)
			}
		})
	}
}

func TestVerify_RejectsInvalidInputs(t *testing.T) {
	if err := Verify(nil, nil); err != ErrCorrupt {
		t.Errorf("Verify(nil, nil) = %v, want ErrCorrupt", err)
	}
	if err := Verify(t.Context(), nil); err != ErrCorrupt {
		t.Errorf("Verify(context, nil) = %v, want ErrCorrupt", err)
	}
}

func projectReader(t *testing.T, recordSize int) *maxminddb.Reader {
	t.Helper()
	tree, err := mmdbwriter.New(mmdbwriter.Options{
		BuildEpoch:              1,
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
	prefix := &net.IPNet{IP: net.IPv4(192, 0, 2, 0), Mask: net.CIDRMask(24, 32)}
	value := mmdbtype.Map{
		"country": mmdbtype.Map{"iso_code": mmdbtype.String("US")},
	}
	if err := tree.Insert(prefix, value); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}

	var data bytes.Buffer
	if _, err := tree.WriteTo(&data); err != nil {
		t.Fatalf("WriteTo() error = %v", err)
	}
	reader, err := maxminddb.OpenBytes(data.Bytes())
	if err != nil {
		t.Fatalf("OpenBytes() error = %v", err)
	}
	return reader
}
