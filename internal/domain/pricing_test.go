package domain_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"muxfleet/internal/domain"
)

func TestCalculatePricing(t *testing.T) {
	t.Run("Calcul nominal Prix HT 4.50 €, TVA 20%", func(t *testing.T) {
		ht, err := domain.ParseDecimal("4.50")
		if err != nil {
			t.Fatalf("ParseDecimal failed: %v", err)
		}
		vat, err := domain.ParseDecimal("20.0")
		if err != nil {
			t.Fatalf("ParseDecimal failed: %v", err)
		}

		pricing, err := domain.CalculatePricing(ht, vat, "EUR", false)
		if err != nil {
			t.Fatalf("CalculatePricing failed: %v", err)
		}

		if pricing.MonthlyHT.String() != "4.50" {
			t.Errorf("MonthlyHT = %v, want 4.50", pricing.MonthlyHT)
		}
		if pricing.MonthlyTTC.String() != "5.40" {
			t.Errorf("MonthlyTTC = %v, want 5.40", pricing.MonthlyTTC)
		}
		if pricing.MonthlyTTC.Cents() != 540 {
			t.Errorf("MonthlyTTC.Cents() = %v, want 540", pricing.MonthlyTTC.Cents())
		}
		if pricing.VATRate.String() != "20.00" {
			t.Errorf("VATRate = %v, want 20.00", pricing.VATRate)
		}

		expectedMonthlyFormatted := "5,40 €/mois"
		if pricing.FormatMonthly() != expectedMonthlyFormatted {
			t.Errorf("FormatMonthly() = %q, want %q", pricing.FormatMonthly(), expectedMonthlyFormatted)
		}

		expectedHourlyFormatted := "0,0074 €/h"
		if pricing.FormatHourly() != expectedHourlyFormatted {
			t.Errorf("FormatHourly() = %q, want %q", pricing.FormatHourly(), expectedHourlyFormatted)
		}

		// Verify JSON serialization uses exact decimal without float drift
		data, err := json.Marshal(pricing)
		if err != nil {
			t.Fatalf("json.Marshal failed: %v", err)
		}
		var unmarshaled domain.Pricing
		if err := json.Unmarshal(data, &unmarshaled); err != nil {
			t.Fatalf("json.Unmarshal failed: %v", err)
		}
		if unmarshaled.MonthlyTTC.String() != "5.40" {
			t.Errorf("unmarshaled MonthlyTTC = %v, want 5.40", unmarshaled.MonthlyTTC)
		}
	})

	t.Run("Prix estimé avec symbole ≈", func(t *testing.T) {
		ht, _ := domain.ParseDecimal("10.00")
		vat, _ := domain.ParseDecimal("20.0")

		pricing, err := domain.CalculatePricing(ht, vat, "EUR", true)
		if err != nil {
			t.Fatalf("CalculatePricing failed: %v", err)
		}

		if pricing.FormatMonthly() != "≈ 12,00 €/mois" {
			t.Errorf("FormatMonthly() = %q, want %q", pricing.FormatMonthly(), "≈ 12,00 €/mois")
		}
		if pricing.FormatHourly() != "≈ 0,0164 €/h" {
			t.Errorf("FormatHourly() = %q, want %q", pricing.FormatHourly(), "≈ 0,0164 €/h")
		}
	})

	t.Run("Refus taux de TVA négatif", func(t *testing.T) {
		ht, _ := domain.ParseDecimal("4.50")
		vat, _ := domain.ParseDecimal("-5.0")

		_, err := domain.CalculatePricing(ht, vat, "EUR", false)
		if err == nil {
			t.Fatal("CalculatePricing with negative VAT rate should fail, got nil")
		}
		if !errors.Is(err, domain.ErrNegativeVATRate) {
			t.Errorf("expected ErrNegativeVATRate, got %v", err)
		}
	})

	t.Run("Refus montant HT négatif", func(t *testing.T) {
		ht, _ := domain.ParseDecimal("-1.00")
		vat, _ := domain.ParseDecimal("20.0")

		_, err := domain.CalculatePricing(ht, vat, "EUR", false)
		if err == nil {
			t.Fatal("CalculatePricing with negative amount should fail, got nil")
		}
		if !errors.Is(err, domain.ErrNegativeAmount) {
			t.Errorf("expected ErrNegativeAmount, got %v", err)
		}
	})

	t.Run("Refus devise vide", func(t *testing.T) {
		ht, _ := domain.ParseDecimal("4.50")
		vat, _ := domain.ParseDecimal("20.0")

		_, err := domain.CalculatePricing(ht, vat, "", false)
		if err == nil {
			t.Fatal("CalculatePricing with empty currency should fail, got nil")
		}
		if !errors.Is(err, domain.ErrEmptyCurrency) {
			t.Errorf("expected ErrEmptyCurrency, got %v", err)
		}
	})
}

func TestParseDecimalStrict(t *testing.T) {
	ok := map[string]string{
		"4.50":         "4.5000",
		"-0.5":         "-0.5000",
		"+2":           "2.0000",
		" 20 ":         "20.0000",
		"1.23456":      "1.2346",
		"1.23454":      "1.2345",
		"-1.23456":     "-1.2346",
		"0.99995":      "1.0000",
		"3.2900000000": "3.2900",
	}
	for in, want := range ok {
		d, err := domain.ParseDecimal(in)
		if err != nil {
			t.Errorf("ParseDecimal(%q) error: %v", in, err)
			continue
		}
		if got := d.StringFixed(4, "."); got != want {
			t.Errorf("ParseDecimal(%q) = %s, want %s", in, got, want)
		}
	}

	for _, in := range []string{"", "1.-5", "--1", "+-1", "-+1", "1.", ".5", "1,5", "1.2.3", "1e3", "abc", "922337203685478"} {
		if _, err := domain.ParseDecimal(in); !errors.Is(err, domain.ErrInvalidDecimal) {
			t.Errorf("ParseDecimal(%q) err = %v, want ErrInvalidDecimal", in, err)
		}
	}
}

func TestDecimalJSONKeepsScale(t *testing.T) {
	ht, _ := domain.ParseDecimal("4.50")
	vat, _ := domain.ParseDecimal("20")
	p, err := domain.CalculatePricing(ht, vat, "EUR", false)
	if err != nil {
		t.Fatal(err)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"monthly_ttc":5.40`, `"hourly_ttc":0.0074`, `"hourly_ht":0.0062`, `"vat_rate":20.00`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("JSON %s does not contain %s", out, want)
		}
	}
	var back domain.Pricing
	if err := json.Unmarshal(out, &back); err != nil {
		t.Fatal(err)
	}
	if back.HourlyTTC != p.HourlyTTC || back.MonthlyTTC != p.MonthlyTTC {
		t.Errorf("round trip lost precision: %s/%s -> %s/%s",
			p.HourlyTTC.StringFixed(4, "."), p.MonthlyTTC.StringFixed(4, "."),
			back.HourlyTTC.StringFixed(4, "."), back.MonthlyTTC.StringFixed(4, "."))
	}
}
