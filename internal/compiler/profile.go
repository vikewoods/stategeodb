package compiler

import (
	"context"

	"github.com/vikewoods/stategeodb/internal/artifactprofile"
	"github.com/vikewoods/stategeodb/internal/source"
)

type projectionStats struct {
	processed                  int
	usSubdivisionsRetained     int
	nonUSSubdivisionsRemoved   int
	recordsWithoutSubdivisions int
}

func projectRecords(ctx context.Context, records []source.Record) (projectionStats, error) {
	var stats projectionStats
	for index, record := range records {
		if err := ctx.Err(); err != nil {
			return projectionStats{}, err
		}

		projected, err := artifactprofile.Project(record)
		if err != nil {
			return projectionStats{}, err
		}
		records[index] = projected
		stats.processed++
		switch {
		case record.Subdivision == "":
			stats.recordsWithoutSubdivisions++
		case record.Country == "US":
			stats.usSubdivisionsRetained++
		default:
			stats.nonUSSubdivisionsRemoved++
		}
	}
	if err := ctx.Err(); err != nil {
		return projectionStats{}, err
	}
	return stats, nil
}
