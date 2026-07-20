package compiler

import (
	"context"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/vikewoods/stategeodb/internal/artifactprofile"
	"github.com/vikewoods/stategeodb/internal/source"
)

func TestProjectRecords(t *testing.T) {
	t.Parallel()

	records := []source.Record{
		profileRecord(t, "192.0.2.0/24", "US", "CA"),
		profileRecord(t, "192.0.3.0/24", "US", ""),
		profileRecord(t, "2001:db8::/32", "GB", "ENG"),
		profileRecord(t, "198.51.100.0/24", "", ""),
	}
	firstAddress := &records[0]
	stats, err := projectRecords(t.Context(), records)
	if err != nil {
		t.Fatalf("projectRecords() error = %v", err)
	}
	expectedStats := projectionStats{
		processed:                  4,
		usSubdivisionsRetained:     1,
		nonUSSubdivisionsRemoved:   1,
		recordsWithoutSubdivisions: 2,
	}
	if stats != expectedStats {
		t.Errorf("projectRecords() stats = %+v, want %+v", stats, expectedStats)
	}
	if &records[0] != firstAddress {
		t.Error("projectRecords() replaced the caller-owned slice storage")
	}
	if records[0].Subdivision != "CA" || records[2].Subdivision != "" {
		t.Errorf("projected subdivisions = %q, %q; want CA and empty", records[0].Subdivision, records[2].Subdivision)
	}

	beforeRepeat := slices.Clone(records)
	repeatedStats, err := projectRecords(t.Context(), records)
	if err != nil {
		t.Fatalf("repeated projectRecords() error = %v", err)
	}
	if !slices.Equal(records, beforeRepeat) {
		t.Errorf("repeated projection changed records: got %+v, want %+v", records, beforeRepeat)
	}
	if repeatedStats.nonUSSubdivisionsRemoved != 0 || repeatedStats.recordsWithoutSubdivisions != 3 {
		t.Errorf("repeated projection stats = %+v, want no removed subdivisions", repeatedStats)
	}
}

func TestProjectRecords_Failures(t *testing.T) {
	t.Parallel()

	invalid := profileRecord(t, "2001:db8::/32", "GB", "ENG")
	invalid.Country = "gb"
	records := []source.Record{invalid}
	stats, err := projectRecords(t.Context(), records)
	if stats != (projectionStats{}) {
		t.Errorf("projectRecords() stats = %+v, want zero", stats)
	}
	for _, target := range []error{artifactprofile.ErrInvalidRecord, source.ErrInvalidCountry} {
		if !errors.Is(err, target) {
			t.Errorf("projectRecords() error = %v, want errors.Is(%v)", err, target)
		}
	}
	if strings.Contains(err.Error(), invalid.Country) {
		t.Errorf("projectRecords() error exposed record value: %v", err)
	}

	canceling := &projectionCancelContext{
		Context:   t.Context(),
		remaining: 1,
	}
	stats, err = projectRecords(canceling, []source.Record{
		profileRecord(t, "192.0.2.0/24", "US", "CA"),
		profileRecord(t, "192.0.3.0/24", "US", "NY"),
	})
	if stats != (projectionStats{}) || !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled projectRecords() = %+v/%v, want zero/context.Canceled", stats, err)
	}
}

type projectionCancelContext struct {
	context.Context
	remaining int
}

func (ctx *projectionCancelContext) Err() error {
	if ctx.remaining == 0 {
		return context.Canceled
	}
	ctx.remaining--
	return nil
}

func profileRecord(t *testing.T, prefix string, country string, subdivision string) source.Record {
	t.Helper()
	record, err := source.NewRecord(
		netip.MustParsePrefix(prefix),
		country,
		subdivision,
		"profile-test",
	)
	if err != nil {
		t.Fatalf("NewRecord() error = %v", err)
	}
	return record
}
