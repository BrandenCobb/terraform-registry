package main

import (
	"sort"
	"strconv"
	"strings"
)

type semanticVersion struct {
	core [3]uint64
	pre  []string
}

// sortSemanticVersions orders valid semantic versions from oldest to newest.
func sortSemanticVersions(versions []string) {
	sort.SliceStable(versions, func(i, j int) bool { return compareSemver(versions[i], versions[j]) < 0 })
}

func compareSemver(a, b string) int {
	va, oka := parseSemanticVersion(a)
	vb, okb := parseSemanticVersion(b)
	if !oka || !okb {
		return strings.Compare(a, b)
	}
	for i := 0; i < 3; i++ {
		if va.core[i] < vb.core[i] {
			return -1
		}
		if va.core[i] > vb.core[i] {
			return 1
		}
	}
	if len(va.pre) == 0 && len(vb.pre) == 0 {
		return 0
	}
	if len(va.pre) == 0 {
		return 1
	}
	if len(vb.pre) == 0 {
		return -1
	}
	for i := 0; i < len(va.pre) && i < len(vb.pre); i++ {
		aID, bID := va.pre[i], vb.pre[i]
		aNum, aNumeric := numericIdentifier(aID)
		bNum, bNumeric := numericIdentifier(bID)
		switch {
		case aNumeric && bNumeric:
			if aNum < bNum {
				return -1
			}
			if aNum > bNum {
				return 1
			}
		case aNumeric:
			return -1
		case bNumeric:
			return 1
		default:
			if c := strings.Compare(aID, bID); c != 0 {
				return c
			}
		}
	}
	if len(va.pre) < len(vb.pre) {
		return -1
	}
	if len(va.pre) > len(vb.pre) {
		return 1
	}
	return 0
}

func parseSemanticVersion(value string) (semanticVersion, bool) {
	var result semanticVersion
	if value == "" || strings.HasPrefix(value, "v") {
		return result, false
	}

	versionAndPre, build, hasBuild := strings.Cut(value, "+")
	if hasBuild && (strings.Contains(build, "+") || !validIdentifiers(build, false)) {
		return result, false
	}
	core, prerelease, hasPrerelease := strings.Cut(versionAndPre, "-")
	if hasPrerelease && !validIdentifiers(prerelease, true) {
		return result, false
	}
	parts := strings.Split(core, ".")
	if len(parts) != 3 {
		return result, false
	}
	for i, part := range parts {
		value, ok := numericIdentifier(part)
		if !ok || (len(part) > 1 && part[0] == '0') {
			return result, false
		}
		result.core[i] = value
	}
	if hasPrerelease {
		result.pre = strings.Split(prerelease, ".")
	}
	return result, true
}

func validIdentifiers(value string, enforceNumericLeadingZero bool) bool {
	if value == "" {
		return false
	}
	for _, identifier := range strings.Split(value, ".") {
		if identifier == "" {
			return false
		}
		allNumeric := true
		for _, char := range identifier {
			if (char < '0' || char > '9') && (char < 'A' || char > 'Z') && (char < 'a' || char > 'z') && char != '-' {
				return false
			}
			if char < '0' || char > '9' {
				allNumeric = false
			}
		}
		if enforceNumericLeadingZero && allNumeric && len(identifier) > 1 && identifier[0] == '0' {
			return false
		}
	}
	return true
}

func numericIdentifier(value string) (uint64, bool) {
	if value == "" {
		return 0, false
	}
	for _, char := range value {
		if char < '0' || char > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseUint(value, 10, 64)
	return n, err == nil
}
