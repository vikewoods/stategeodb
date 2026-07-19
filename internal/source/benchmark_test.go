package source_test

import (
	"net/netip"
	"testing"

	"github.com/vikewoods/stategeodb/internal/source"
)

func BenchmarkNormalizeLocation(b *testing.B) {
	tests := []struct {
		name        string
		country     string
		subdivision string
	}{
		{name: "canonical", country: "US", subdivision: "CA"},
		{name: "lowercase", country: "us", subdivision: "ca"},
	}

	for _, test := range tests {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, _, err := source.NormalizeLocation(test.country, test.subdivision)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkNewRecord(b *testing.B) {
	prefix := netip.MustParsePrefix("192.0.2.0/24")
	tests := []struct {
		name        string
		country     string
		subdivision string
	}{
		{name: "canonical", country: "US", subdivision: "CA"},
		{name: "lowercase", country: "us", subdivision: "ca"},
	}

	for _, test := range tests {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				_, err := source.NewRecord(
					prefix,
					test.country,
					test.subdivision,
					"primary",
				)
				if err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
