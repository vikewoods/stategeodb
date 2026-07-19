package source

import (
	"fmt"
	"net/netip"
	"strings"
	"unicode/utf8"
)

const mappedIPv4PrefixBits = 96

// NormalizePrefix returns prefix in canonical network form. IPv4-mapped IPv6
// prefixes wholly contained by ::ffff:0:0/96 are converted to native IPv4.
func NormalizePrefix(prefix netip.Prefix) (netip.Prefix, error) {
	if !prefix.IsValid() {
		return netip.Prefix{}, invalid(ErrInvalidPrefix, "value is not initialized")
	}

	if prefix.Addr().Is4In6() {
		if prefix.Bits() < mappedIPv4PrefixBits {
			return netip.Prefix{}, invalid(
				ErrInvalidPrefix,
				"mapped ipv4 prefix length is less than 96",
			)
		}

		prefix = netip.PrefixFrom(
			prefix.Addr().Unmap(),
			prefix.Bits()-mappedIPv4PrefixBits,
		)
	}

	return prefix.Masked(), nil
}

// NormalizeLocation validates and normalizes a country and its optional first
// subdivision. Empty values represent an unknown location.
func NormalizeLocation(country string, subdivision string) (string, string, error) {
	normalizedCountry, ok := normalizeCode(country, 2, 2, false)
	if !ok {
		return "", "", invalid(
			ErrInvalidCountry,
			"expected empty or two ascii letters",
		)
	}

	if subdivision != "" && normalizedCountry == "" {
		return "", "", invalid(ErrInvalidSubdivision, "country is required")
	}

	normalizedSubdivision, ok := normalizeCode(subdivision, 1, 3, true)
	if !ok {
		return "", "", invalid(
			ErrInvalidSubdivision,
			"expected empty or one to three ascii letters or digits",
		)
	}

	return normalizedCountry, normalizedSubdivision, nil
}

// ValidateSourceID validates a stable logical source identifier without
// changing its case or deriving it from another value.
func ValidateSourceID(sourceID string) error {
	if sourceID == "" {
		return invalid(ErrInvalidSourceID, "value is empty")
	}
	if !utf8.ValidString(sourceID) {
		return invalid(ErrInvalidSourceID, "value is not valid utf-8")
	}
	if strings.TrimSpace(sourceID) != sourceID {
		return invalid(ErrInvalidSourceID, "leading or trailing whitespace")
	}
	for i := range len(sourceID) {
		character := sourceID[i]
		if character < 0x20 || character == 0x7f {
			return invalid(ErrInvalidSourceID, "ascii control character")
		}
		if !isSourceIDCharacter(character) {
			return invalid(
				ErrInvalidSourceID,
				"expected ascii letters, digits, hyphens, underscores, or periods",
			)
		}
	}
	if !isASCIIAlphanumeric(sourceID[0]) || !isASCIIAlphanumeric(sourceID[len(sourceID)-1]) {
		return invalid(ErrInvalidSourceID, "must begin and end with a letter or digit")
	}

	return nil
}

func isSourceIDCharacter(character byte) bool {
	return isASCIIAlphanumeric(character) || character == '-' || character == '_' || character == '.'
}

func isASCIIAlphanumeric(character byte) bool {
	return character >= 'A' && character <= 'Z' ||
		character >= 'a' && character <= 'z' ||
		character >= '0' && character <= '9'
}

func normalizeCode(value string, minLength int, maxLength int, allowDigits bool) (string, bool) {
	if value == "" {
		return "", true
	}
	if len(value) < minLength || len(value) > maxLength {
		return "", false
	}

	requiresNormalization := false
	for i := range len(value) {
		character := value[i]
		switch {
		case character >= 'A' && character <= 'Z':
		case character >= 'a' && character <= 'z':
			requiresNormalization = true
		case allowDigits && character >= '0' && character <= '9':
		default:
			return "", false
		}
	}
	if !requiresNormalization {
		return value, true
	}

	normalized := []byte(value)
	for i := range len(normalized) {
		if normalized[i] >= 'a' && normalized[i] <= 'z' {
			normalized[i] -= 'a' - 'A'
		}
	}

	return string(normalized), true
}

func invalid(classification error, reason string) error {
	return fmt.Errorf("%w: %s", classification, reason)
}
