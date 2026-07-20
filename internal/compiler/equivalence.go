package compiler

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"iter"
	"net/netip"
	"slices"

	"github.com/vikewoods/stategeodb/internal/artifactprofile"
	"github.com/vikewoods/stategeodb/internal/source"
)

// ErrNotEquivalent classifies a failure to prove exact source/candidate
// behavioral equivalence.
var ErrNotEquivalent = errors.New("compiler: candidate is not equivalent")

type networkResult interface {
	Err() error
	Prefix() netip.Prefix
	DecodePath(any, ...any) error
}

type networkIterator = iter.Seq[networkResult]

type addressFamily uint8

const (
	addressFamilyIPv4 addressFamily = 4
	addressFamilyIPv6 addressFamily = 6
)

type behaviorInterval struct {
	family      addressFamily
	start       netip.Addr
	end         netip.Addr
	country     string
	subdivision string
}

func readCandidateRecords(
	ctx context.Context,
	networks networkIterator,
	sourceID string,
) ([]source.Record, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	records := []source.Record{}
	for result := range networks {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := result.Err(); err != nil {
			return nil, classified("traverse candidate networks", ErrNotEquivalent)
		}
		prefix := result.Prefix()
		prefixOnly := source.Record{Prefix: prefix, SourceID: sourceID}
		if err := prefixOnly.Validate(); err != nil {
			return nil, classifiedWithCause(
				"validate candidate prefix",
				ErrNotEquivalent,
				err,
			)
		}

		var country *string
		if err := result.DecodePath(&country, "country", "iso_code"); err != nil {
			return nil, classified("decode candidate country", ErrNotEquivalent)
		}
		var subdivision *string
		if err := result.DecodePath(&subdivision, "subdivisions", 0, "iso_code"); err != nil {
			return nil, classified("decode candidate subdivision", ErrNotEquivalent)
		}

		rawCountry := optionalString(country)
		rawSubdivision := optionalString(subdivision)
		record, err := source.NewRecord(
			prefix,
			rawCountry,
			rawSubdivision,
			sourceID,
		)
		if err != nil {
			return nil, classifiedWithCause("normalize candidate record", ErrNotEquivalent, err)
		}
		if record.Country != rawCountry || record.Subdivision != rawSubdivision {
			raw := record
			raw.Country = rawCountry
			raw.Subdivision = rawSubdivision
			return nil, classifiedWithCause(
				"validate candidate location",
				ErrNotEquivalent,
				raw.Validate(),
			)
		}
		if err := artifactprofile.Validate(record); err != nil {
			return nil, classifiedWithCause(
				"validate candidate artifact profile",
				ErrNotEquivalent,
				err,
			)
		}
		records = append(records, record)
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return records, nil
}

func compareRecordBehavior(
	ctx context.Context,
	sourceRecords []source.Record,
	outputRecords []source.Record,
) (EquivalenceStats, error) {
	stats := EquivalenceStats{
		SourceRecords:  len(sourceRecords),
		OutputNetworks: len(outputRecords),
	}
	if err := ctx.Err(); err != nil {
		return EquivalenceStats{}, err
	}

	sourceIntervals, err := makeIntervals(ctx, sourceRecords, "source")
	if err != nil {
		return EquivalenceStats{}, err
	}
	outputIntervals, err := makeIntervals(ctx, outputRecords, "output")
	if err != nil {
		return EquivalenceStats{}, err
	}
	if err := ctx.Err(); err != nil {
		return EquivalenceStats{}, err
	}

	slices.SortFunc(sourceIntervals, compareIntervals)
	slices.SortFunc(outputIntervals, compareIntervals)
	if err := ctx.Err(); err != nil {
		return EquivalenceStats{}, err
	}
	if err := validateIntervalStream(ctx, sourceIntervals, "source"); err != nil {
		return EquivalenceStats{}, err
	}
	if err := validateIntervalStream(ctx, outputIntervals, "output"); err != nil {
		return EquivalenceStats{}, err
	}

	for _, family := range []addressFamily{addressFamilyIPv4, addressFamilyIPv6} {
		compared, err := compareFamilyIntervals(
			ctx,
			intervalsForFamily(sourceIntervals, family),
			intervalsForFamily(outputIntervals, family),
			family,
		)
		if err != nil {
			return EquivalenceStats{}, err
		}
		stats.ComparedSegments += compared
	}
	if err := ctx.Err(); err != nil {
		return EquivalenceStats{}, err
	}
	return stats, nil
}

func makeIntervals(
	ctx context.Context,
	records []source.Record,
	streamName string,
) ([]behaviorInterval, error) {
	intervals := make([]behaviorInterval, 0, len(records))
	for _, record := range records {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if err := artifactprofile.Validate(record); err != nil {
			return nil, classifiedWithCause(
				"validate "+streamName+" interval",
				ErrNotEquivalent,
				err,
			)
		}

		intervals = append(intervals, behaviorInterval{
			family:      familyOf(record.Prefix),
			start:       record.Prefix.Addr(),
			end:         finalAddress(record.Prefix),
			country:     record.Country,
			subdivision: record.Subdivision,
		})
	}
	return intervals, nil
}

func finalAddress(prefix netip.Prefix) netip.Addr {
	if prefix.Addr().Is4() {
		address := prefix.Addr().As4()
		for bit := prefix.Bits(); bit < 32; bit++ {
			address[bit/8] |= byte(1 << (7 - bit%8))
		}
		return netip.AddrFrom4(address)
	}

	address := prefix.Addr().As16()
	for bit := prefix.Bits(); bit < 128; bit++ {
		address[bit/8] |= byte(1 << (7 - bit%8))
	}
	return netip.AddrFrom16(address)
}

func familyOf(prefix netip.Prefix) addressFamily {
	if prefix.Addr().Is4() {
		return addressFamilyIPv4
	}
	return addressFamilyIPv6
}

func compareIntervals(left behaviorInterval, right behaviorInterval) int {
	if result := cmp.Compare(left.family, right.family); result != 0 {
		return result
	}
	if result := left.start.Compare(right.start); result != 0 {
		return result
	}
	if result := left.end.Compare(right.end); result != 0 {
		return result
	}
	if result := cmp.Compare(left.country, right.country); result != 0 {
		return result
	}
	return cmp.Compare(left.subdivision, right.subdivision)
}

func validateIntervalStream(
	ctx context.Context,
	intervals []behaviorInterval,
	streamName string,
) error {
	for index := range intervals {
		if err := ctx.Err(); err != nil {
			return err
		}
		if index == 0 || intervals[index-1].family != intervals[index].family {
			continue
		}
		if intervals[index-1].end.Compare(intervals[index].start) >= 0 {
			return classified(
				fmt.Sprintf(
					"validate overlapping %s %s intervals",
					streamName,
					intervals[index].family,
				),
				ErrNotEquivalent,
			)
		}
	}
	return nil
}

func intervalsForFamily(
	intervals []behaviorInterval,
	family addressFamily,
) []behaviorInterval {
	start, _ := slices.BinarySearchFunc(intervals, family, func(
		interval behaviorInterval,
		target addressFamily,
	) int {
		return cmp.Compare(interval.family, target)
	})
	end := start
	for end < len(intervals) && intervals[end].family == family {
		end++
	}
	return intervals[start:end]
}

func compareFamilyIntervals(
	ctx context.Context,
	sourceIntervals []behaviorInterval,
	outputIntervals []behaviorInterval,
	family addressFamily,
) (int, error) {
	var sourceIndex, outputIndex, comparedSegments int
	var sourceStart, outputStart netip.Addr

	for sourceIndex < len(sourceIntervals) && outputIndex < len(outputIntervals) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		sourceInterval := sourceIntervals[sourceIndex]
		outputInterval := outputIntervals[outputIndex]
		if !sourceStart.IsValid() {
			sourceStart = sourceInterval.start
		}
		if !outputStart.IsValid() {
			outputStart = outputInterval.start
		}

		if result := sourceStart.Compare(outputStart); result != 0 {
			if result < 0 {
				return 0, presenceMismatch(family, sourceStart, sourceInterval.end, true)
			}
			return 0, presenceMismatch(family, outputStart, outputInterval.end, false)
		}

		segmentEnd := sourceInterval.end
		if outputInterval.end.Less(segmentEnd) {
			segmentEnd = outputInterval.end
		}
		comparedSegments++
		if sourceInterval.country != outputInterval.country ||
			sourceInterval.subdivision != outputInterval.subdivision {
			return 0, classified(
				fmt.Sprintf(
					"compare %s segment %s-%s location %q/%q with %q/%q",
					family,
					sourceStart,
					segmentEnd,
					sourceInterval.country,
					sourceInterval.subdivision,
					outputInterval.country,
					outputInterval.subdivision,
				),
				ErrNotEquivalent,
			)
		}

		switch sourceInterval.end.Compare(outputInterval.end) {
		case -1:
			sourceIndex++
			sourceStart = netip.Addr{}
			outputStart = segmentEnd.Next()
		case 1:
			outputIndex++
			outputStart = netip.Addr{}
			sourceStart = segmentEnd.Next()
		default:
			sourceIndex++
			outputIndex++
			sourceStart = netip.Addr{}
			outputStart = netip.Addr{}
		}
	}

	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if sourceIndex < len(sourceIntervals) {
		start := sourceStart
		if !start.IsValid() {
			start = sourceIntervals[sourceIndex].start
		}
		return 0, presenceMismatch(
			family,
			start,
			sourceIntervals[sourceIndex].end,
			true,
		)
	}
	if outputIndex < len(outputIntervals) {
		start := outputStart
		if !start.IsValid() {
			start = outputIntervals[outputIndex].start
		}
		return 0, presenceMismatch(
			family,
			start,
			outputIntervals[outputIndex].end,
			false,
		)
	}
	return comparedSegments, nil
}

func presenceMismatch(
	family addressFamily,
	start netip.Addr,
	end netip.Addr,
	sourcePresent bool,
) error {
	expected := "absent"
	actual := "present"
	if sourcePresent {
		expected = "present"
		actual = "absent"
	}
	return classified(
		fmt.Sprintf(
			"compare %s segment %s-%s expected %s actual %s",
			family,
			start,
			end,
			expected,
			actual,
		),
		ErrNotEquivalent,
	)
}

func optionalString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

func (family addressFamily) String() string {
	if family == addressFamilyIPv4 {
		return "ipv4"
	}
	return "ipv6"
}
