// Package source defines provider-neutral records produced by source adapters.
// It contains no filesystem, MMDB, merge, or access-control behavior.
package source

import "errors"

var (
	// ErrInvalidPrefix classifies invalid or noncanonical network prefixes.
	ErrInvalidPrefix = errors.New("source: invalid prefix")
	// ErrInvalidCountry classifies invalid country codes.
	ErrInvalidCountry = errors.New("source: invalid country")
	// ErrInvalidSubdivision classifies invalid first-subdivision codes.
	ErrInvalidSubdivision = errors.New("source: invalid subdivision")
	// ErrInvalidSourceID classifies invalid logical source identifiers.
	ErrInvalidSourceID = errors.New("source: invalid source id")
)
