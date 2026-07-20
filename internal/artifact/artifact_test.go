package artifact

import (
	"testing"

	maxminddb "github.com/oschwald/maxminddb-golang/v2"

	"github.com/vikewoods/stategeodb/internal/mmdb"
)

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
		RecordSize:               28,
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
		{name: "record size", mutate: func(metadata *maxminddb.Metadata) { metadata.RecordSize = 24 }},
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

func TestVerify_RejectsInvalidInputs(t *testing.T) {
	if err := Verify(nil, nil); err != ErrCorrupt {
		t.Errorf("Verify(nil, nil) = %v, want ErrCorrupt", err)
	}
	if err := Verify(t.Context(), nil); err != ErrCorrupt {
		t.Errorf("Verify(context, nil) = %v, want ErrCorrupt", err)
	}
}
