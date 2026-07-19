package source_test

import (
	"errors"
	"net/netip"
	"strings"
	"testing"

	"github.com/vikewoods/stategeodb/internal/source"
)

func TestNormalizeLocation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		country             string
		subdivision         string
		expectedCountry     string
		expectedSubdivision string
		expectedError       error
	}{
		{name: "unknown location"},
		{name: "uppercase country", country: "US", expectedCountry: "US"},
		{name: "lowercase country", country: "gb", expectedCountry: "GB"},
		{name: "uppercase subdivision", country: "US", subdivision: "CA", expectedCountry: "US", expectedSubdivision: "CA"},
		{name: "lowercase subdivision", country: "GB", subdivision: "eng", expectedCountry: "GB", expectedSubdivision: "ENG"},
		{name: "numeric subdivision", country: "BR", subdivision: "11", expectedCountry: "BR", expectedSubdivision: "11"},
		{name: "unknown subdivision", country: "US", expectedCountry: "US"},
		{name: "short country", country: "U", expectedError: source.ErrInvalidCountry},
		{name: "long country", country: "USA", expectedError: source.ErrInvalidCountry},
		{name: "country digit", country: "U1", expectedError: source.ErrInvalidCountry},
		{name: "country punctuation", country: "U-", expectedError: source.ErrInvalidCountry},
		{name: "country whitespace", country: " US", expectedError: source.ErrInvalidCountry},
		{name: "unicode country", country: "ÅA", expectedError: source.ErrInvalidCountry},
		{name: "subdivision without country", subdivision: "CA", expectedError: source.ErrInvalidSubdivision},
		{name: "full subdivision code", country: "US", subdivision: "US-CA", expectedError: source.ErrInvalidSubdivision},
		{name: "long subdivision", country: "US", subdivision: "ABCD", expectedError: source.ErrInvalidSubdivision},
		{name: "subdivision punctuation", country: "US", subdivision: "A_", expectedError: source.ErrInvalidSubdivision},
		{name: "subdivision whitespace", country: "US", subdivision: " CA", expectedError: source.ErrInvalidSubdivision},
		{name: "unicode subdivision", country: "US", subdivision: "Å", expectedError: source.ErrInvalidSubdivision},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			country, subdivision, err := source.NormalizeLocation(test.country, test.subdivision)
			if !errors.Is(err, test.expectedError) {
				t.Fatalf("NormalizeLocation() error = %v, want classification %v", err, test.expectedError)
			}
			if country != test.expectedCountry {
				t.Errorf("NormalizeLocation() country = %q, want %q", country, test.expectedCountry)
			}
			if subdivision != test.expectedSubdivision {
				t.Errorf("NormalizeLocation() subdivision = %q, want %q", subdivision, test.expectedSubdivision)
			}
		})
	}
}

func TestNormalizePrefix(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		input         netip.Prefix
		expected      netip.Prefix
		expectedError error
	}{
		{
			name:     "canonical ipv4 masking",
			input:    netip.MustParsePrefix("192.0.2.129/24"),
			expected: netip.MustParsePrefix("192.0.2.0/24"),
		},
		{
			name:     "canonical ipv6 masking",
			input:    netip.MustParsePrefix("2001:db8::1234/64"),
			expected: netip.MustParsePrefix("2001:db8::/64"),
		},
		{
			name:     "mapped ipv4 conversion",
			input:    netip.MustParsePrefix("::ffff:192.0.2.129/120"),
			expected: netip.MustParsePrefix("192.0.2.0/24"),
		},
		{
			name:     "mapped prefix bit adjustment",
			input:    netip.MustParsePrefix("::ffff:192.0.2.129/112"),
			expected: netip.MustParsePrefix("192.0.0.0/16"),
		},
		{
			name:     "complete mapped range",
			input:    netip.MustParsePrefix("::ffff:192.0.2.129/96"),
			expected: netip.MustParsePrefix("0.0.0.0/0"),
		},
		{
			name:     "native ipv6 preservation",
			input:    netip.MustParsePrefix("2001:db8::c000:281/120"),
			expected: netip.MustParsePrefix("2001:db8::c000:200/120"),
		},
		{
			name:          "invalid zero prefix",
			expectedError: source.ErrInvalidPrefix,
		},
		{
			name:          "mapped prefix outside mapped range",
			input:         netip.MustParsePrefix("::ffff:192.0.2.129/95"),
			expectedError: source.ErrInvalidPrefix,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			actual, err := source.NormalizePrefix(test.input)
			if !errors.Is(err, test.expectedError) {
				t.Fatalf("NormalizePrefix() error = %v, want classification %v", err, test.expectedError)
			}
			if actual != test.expected {
				t.Errorf("NormalizePrefix() = %v, want %v", actual, test.expected)
			}
		})
	}
}

func TestValidateSourceID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		sourceID      string
		expectedError error
	}{
		{name: "primary", sourceID: "primary"},
		{name: "maxmind city", sourceID: "maxmind-city"},
		{name: "secondary review", sourceID: "secondary-review"},
		{name: "case preserved", sourceID: "Primary-City"},
		{name: "supported separators", sourceID: "source_review.v1"},
		{name: "empty", expectedError: source.ErrInvalidSourceID},
		{name: "invalid utf-8", sourceID: "source-\xff", expectedError: source.ErrInvalidSourceID},
		{name: "leading whitespace", sourceID: " primary", expectedError: source.ErrInvalidSourceID},
		{name: "trailing whitespace", sourceID: "primary ", expectedError: source.ErrInvalidSourceID},
		{name: "unicode surrounding whitespace", sourceID: "\u2003primary", expectedError: source.ErrInvalidSourceID},
		{name: "embedded null", sourceID: "primary\x00forged", expectedError: source.ErrInvalidSourceID},
		{name: "embedded newline", sourceID: "primary\nforged", expectedError: source.ErrInvalidSourceID},
		{name: "delete control", sourceID: "primary\x7fforged", expectedError: source.ErrInvalidSourceID},
		{name: "unicode letter", sourceID: "source-é", expectedError: source.ErrInvalidSourceID},
		{name: "unicode control", sourceID: "primary\u0085forged", expectedError: source.ErrInvalidSourceID},
		{name: "unicode line separator", sourceID: "primary\u2028forged", expectedError: source.ErrInvalidSourceID},
		{name: "bidi override", sourceID: "primary\u202eforged", expectedError: source.ErrInvalidSourceID},
		{name: "relative path", sourceID: "../primary", expectedError: source.ErrInvalidSourceID},
		{name: "absolute path", sourceID: "/data/primary", expectedError: source.ErrInvalidSourceID},
		{name: "windows path", sourceID: `C:\data\primary`, expectedError: source.ErrInvalidSourceID},
		{name: "authenticated url", sourceID: "https://user:secret@example.com/source", expectedError: source.ErrInvalidSourceID},
		{name: "leading separator", sourceID: "-primary", expectedError: source.ErrInvalidSourceID},
		{name: "trailing separator", sourceID: "primary-", expectedError: source.ErrInvalidSourceID},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := source.ValidateSourceID(test.sourceID)
			if !errors.Is(err, test.expectedError) {
				t.Fatalf("ValidateSourceID() error = %v, want classification %v", err, test.expectedError)
			}
			if err != nil && test.sourceID != "" && strings.Contains(err.Error(), test.sourceID) {
				t.Errorf("ValidateSourceID() error echoed unsafe source ID")
			}
		})
	}
}
