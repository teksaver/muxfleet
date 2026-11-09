package domain_test

import (
	"errors"
	"testing"

	"muxfleet/internal/domain"
)

func TestSemVerCompare(t *testing.T) {
	tests := []struct {
		v1       string
		v2       string
		expected int
	}{
		{"1.0.0", "1.0.0", 0},
		{"v1.0.0", "1.0.0", 0},
		{"0.1.0", "0.2.0", -1},
		{"1.2.3", "1.2.2", 1},
		{"2.0.0", "1.99.99", 1},
		{"0.1.0", "1.0.0", -1},
		{"1.2.3", "1.3.0", -1},
	}

	for _, tc := range tests {
		s1, err := domain.ParseSemVer(tc.v1)
		if err != nil {
			t.Fatalf("ParseSemVer(%q) failed: %v", tc.v1, err)
		}
		s2, err := domain.ParseSemVer(tc.v2)
		if err != nil {
			t.Fatalf("ParseSemVer(%q) failed: %v", tc.v2, err)
		}

		got := s1.Compare(s2)
		if got != tc.expected {
			t.Errorf("Compare(%q, %q) = %d, want %d", tc.v1, tc.v2, got, tc.expected)
		}
	}
}

func TestCheckUpgradeAllowed(t *testing.T) {
	t.Run("Version distante absente => autorisé", func(t *testing.T) {
		err := domain.CheckUpgradeAllowed("", "0.1.0")
		if err != nil {
			t.Errorf("CheckUpgradeAllowed with empty remote failed: %v", err)
		}
	})

	t.Run("Version distante < version locale => autorisé", func(t *testing.T) {
		err := domain.CheckUpgradeAllowed("0.1.0", "0.2.0")
		if err != nil {
			t.Errorf("CheckUpgradeAllowed upgrade failed: %v", err)
		}
	})

	t.Run("Version distante == version locale => autorisé", func(t *testing.T) {
		err := domain.CheckUpgradeAllowed("0.1.0", "0.1.0")
		if err != nil {
			t.Errorf("CheckUpgradeAllowed equal failed: %v", err)
		}
	})

	t.Run("Version distante > version locale => refus de rétrogradation (R9)", func(t *testing.T) {
		err := domain.CheckUpgradeAllowed("0.2.0", "0.1.0")
		if err == nil {
			t.Fatal("CheckUpgradeAllowed with remote > local should fail, got nil")
		}
		if !errors.Is(err, domain.ErrDowngradeRefused) {
			t.Errorf("expected ErrDowngradeRefused, got %v", err)
		}
	})

	t.Run("Versions invalides", func(t *testing.T) {
		err := domain.CheckUpgradeAllowed("invalid", "0.1.0")
		if err == nil {
			t.Error("expected error for invalid remote version")
		}

		err = domain.CheckUpgradeAllowed("0.1.0", "invalid")
		if err == nil {
			t.Error("expected error for invalid local version")
		}
	})
}

func TestPrjVersionFromBinary(t *testing.T) {
	marker := func(v string) string {
		return domain.PrjVersionMarkerPrefix + v + domain.PrjVersionMarkerSuffix
	}

	t.Run("Marqueur noyé dans des octets quelconques", func(t *testing.T) {
		bin := []byte("\x7fELF\x00\x01" + marker("1.2.3") + "\x00suite")
		got, err := domain.PrjVersionFromBinary(bin)
		if err != nil || got != "1.2.3" {
			t.Fatalf("got (%q, %v), want (1.2.3, nil)", got, err)
		}
	})

	t.Run("Marqueur répété à l'identique", func(t *testing.T) {
		bin := []byte(marker("0.1.0") + "--" + marker("0.1.0"))
		got, err := domain.PrjVersionFromBinary(bin)
		if err != nil || got != "0.1.0" {
			t.Fatalf("got (%q, %v), want (0.1.0, nil)", got, err)
		}
	})

	t.Run("Préfixe isolé ou version invalide ignorés", func(t *testing.T) {
		bin := []byte(domain.PrjVersionMarkerPrefix + "pas-une-version" + domain.PrjVersionMarkerSuffix +
			domain.PrjVersionMarkerPrefix + "sans suffixe proche" + string(make([]byte, 80)) +
			marker("0.3.1"))
		got, err := domain.PrjVersionFromBinary(bin)
		if err != nil || got != "0.3.1" {
			t.Fatalf("got (%q, %v), want (0.3.1, nil)", got, err)
		}
	})

	t.Run("Aucun marqueur", func(t *testing.T) {
		_, err := domain.PrjVersionFromBinary([]byte("#!/bin/sh\necho prj\n"))
		if !errors.Is(err, domain.ErrNoVersionMarker) {
			t.Fatalf("expected ErrNoVersionMarker, got %v", err)
		}
	})

	t.Run("Deux versions différentes", func(t *testing.T) {
		_, err := domain.PrjVersionFromBinary([]byte(marker("0.1.0") + marker("0.2.0")))
		if !errors.Is(err, domain.ErrAmbiguousVersionMarker) {
			t.Fatalf("expected ErrAmbiguousVersionMarker, got %v", err)
		}
	})
}
