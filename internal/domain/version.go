package domain

import (
	"bytes"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

var (
	ErrDowngradeRefused       = errors.New("refus de rétrogradation : la version distante est supérieure à la version locale")
	ErrInvalidVersion         = errors.New("version invalide")
	ErrNoVersionMarker        = errors.New("marqueur de version absent du binaire prj")
	ErrAmbiguousVersionMarker = errors.New("plusieurs marqueurs de version différents dans le binaire prj")
)

// Marqueur inscrit tel quel dans les octets du binaire prj, pour que vps lise la version
// du binaire qu'il pousse au lieu de la supposer (AD-5, R9).
const (
	PrjVersionMarkerPrefix = "muxfleet-prj-version=<"
	PrjVersionMarkerSuffix = ">"
)

// maxMarkerVersionLen borne la lecture après le préfixe du marqueur.
const maxMarkerVersionLen = 64

// SemVer represents a semantic version (Major.Minor.Patch).
type SemVer struct {
	Major int
	Minor int
	Patch int
	Raw   string
}

// ParseSemVer parses a semver string like "1.2.3" or "v1.2.3".
func ParseSemVer(v string) (*SemVer, error) {
	clean := strings.TrimSpace(v)
	clean = strings.TrimPrefix(clean, "v")
	clean = strings.TrimPrefix(clean, "V")

	// Cut off pre-release or build metadata if any
	parts := strings.SplitN(clean, "-", 2)
	core := parts[0]

	numStrs := strings.Split(core, ".")
	if len(numStrs) != 3 {
		return nil, fmt.Errorf("%w: %q (format attendu X.Y.Z)", ErrInvalidVersion, v)
	}

	major, err := strconv.Atoi(numStrs[0])
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidVersion, v)
	}
	minor, err := strconv.Atoi(numStrs[1])
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidVersion, v)
	}
	patch, err := strconv.Atoi(numStrs[2])
	if err != nil {
		return nil, fmt.Errorf("%w: %q", ErrInvalidVersion, v)
	}

	return &SemVer{
		Major: major,
		Minor: minor,
		Patch: patch,
		Raw:   v,
	}, nil
}

// Compare compares two semantic versions.
// Returns -1 if s < other, 0 if s == other, 1 if s > other.
func (s *SemVer) Compare(other *SemVer) int {
	if s.Major != other.Major {
		if s.Major < other.Major {
			return -1
		}
		return 1
	}
	if s.Minor != other.Minor {
		if s.Minor < other.Minor {
			return -1
		}
		return 1
	}
	if s.Patch != other.Patch {
		if s.Patch < other.Patch {
			return -1
		}
		return 1
	}
	return 0
}

// CheckUpgradeAllowed verifies that localVersion >= currentRemoteVersion (R9).
// If currentRemoteVersion is empty, upgrade is allowed.
// If currentRemoteVersion > localVersion, returns ErrDowngradeRefused.
func CheckUpgradeAllowed(currentRemoteVersion, localVersion string) error {
	if strings.TrimSpace(currentRemoteVersion) == "" {
		return nil // Version distante absente : poussée autorisée
	}

	remote, err := ParseSemVer(currentRemoteVersion)
	if err != nil {
		return fmt.Errorf("version distante invalide: %w", err)
	}

	local, err := ParseSemVer(localVersion)
	if err != nil {
		return fmt.Errorf("version locale invalide: %w", err)
	}

	if remote.Compare(local) > 0 {
		return fmt.Errorf("%w (distante: %s, locale: %s)", ErrDowngradeRefused, currentRemoteVersion, localVersion)
	}

	return nil
}

// PrjVersionFromBinary lit la version portée par le marqueur d'un binaire prj (AD-5, R9).
// Une occurrence du préfixe sans suffixe proche ou sans version X.Y.Z valide est ignorée.
// Aucun marqueur valide donne ErrNoVersionMarker ; deux versions différentes, ErrAmbiguousVersionMarker.
func PrjVersionFromBinary(bin []byte) (string, error) {
	prefix := []byte(PrjVersionMarkerPrefix)
	suffix := []byte(PrjVersionMarkerSuffix)

	found := ""
	rest := bin
	for {
		i := bytes.Index(rest, prefix)
		if i < 0 {
			break
		}
		rest = rest[i+len(prefix):]

		window := rest
		if len(window) > maxMarkerVersionLen {
			window = window[:maxMarkerVersionLen]
		}
		j := bytes.Index(window, suffix)
		if j <= 0 {
			continue
		}
		candidate := string(window[:j])
		if _, err := ParseSemVer(candidate); err != nil {
			continue
		}
		if found != "" && found != candidate {
			return "", fmt.Errorf("%w (%s, %s)", ErrAmbiguousVersionMarker, found, candidate)
		}
		found = candidate
	}

	if found == "" {
		return "", ErrNoVersionMarker
	}
	return found, nil
}
