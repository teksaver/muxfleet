package domain

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

var (
	ErrNegativeVATRate = errors.New("taux de TVA négatif refusé")
	ErrNegativeAmount  = errors.New("montant HT négatif refusé")
	ErrEmptyCurrency   = errors.New("devise obligatoire")
	ErrInvalidDecimal  = errors.New("nombre décimal invalide")
)

// Scale is the fixed-point scaling factor (10^4 = 10000, 4 decimal places).
const Scale = 10000

// HoursPerMonth is the standard monthly hour count for cloud pricing (365 * 24 / 12 = 730).
const HoursPerMonth = 730

// Decimal represents an exact fixed-point decimal number scaled by 10000 (AD-6).
// No floating-point operations are used in storage or arithmetic.
type Decimal struct {
	scaled int64
}

// NewDecimal creates a Decimal directly from a scaled int64.
func NewDecimal(scaled int64) Decimal {
	return Decimal{scaled: scaled}
}

// NewDecimalFromCents creates a Decimal from integer cents (e.g. 450 cents = 4.50).
func NewDecimalFromCents(cents int64) Decimal {
	return Decimal{scaled: cents * 100}
}

// maxDecimalIntPart keeps intPart*Scale plus a rounded fraction inside int64.
const maxDecimalIntPart = (math.MaxInt64 - Scale) / Scale

// ParseDecimal parses an exact decimal string (e.g. "4.50", "20", "0.0074").
// Grammar: optional sign, digits, optionally "." followed by digits. Digits beyond the
// fourth decimal are rounded half away from zero (provider APIs send up to 10 decimals).
func ParseDecimal(s string) (Decimal, error) {
	raw := s
	s = strings.TrimSpace(s)

	neg := false
	if strings.HasPrefix(s, "-") {
		neg = true
		s = s[1:]
	} else if strings.HasPrefix(s, "+") {
		s = s[1:]
	}

	intStr, fracStr, hasDot := strings.Cut(s, ".")
	if !isDigits(intStr) || (hasDot && !isDigits(fracStr)) {
		return Decimal{}, fmt.Errorf("%w: %q", ErrInvalidDecimal, raw)
	}

	intPart, err := strconv.ParseInt(intStr, 10, 64)
	if err != nil || intPart > maxDecimalIntPart {
		return Decimal{}, fmt.Errorf("%w: %q (dépassement de capacité)", ErrInvalidDecimal, raw)
	}

	var fracPart int64
	if hasDot {
		digits := fracStr
		roundUp := len(digits) > 4 && digits[4] >= '5'
		if len(digits) > 4 {
			digits = digits[:4]
		}
		for len(digits) < 4 {
			digits += "0"
		}
		fracPart, _ = strconv.ParseInt(digits, 10, 64) // 4 ASCII digits, cannot fail
		if roundUp {
			fracPart++
		}
	}

	scaled := intPart*Scale + fracPart
	if neg {
		scaled = -scaled
	}

	return Decimal{scaled: scaled}, nil
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// Scaled returns the underlying scaled int64 value.
func (d Decimal) Scaled() int64 {
	return d.scaled
}

// Cents returns the rounded amount in cents (2 decimal places).
func (d Decimal) Cents() int64 {
	if d.scaled >= 0 {
		return (d.scaled + 50) / 100
	}
	return (d.scaled - 50) / 100
}

// StringFixed returns the decimal string with a fixed number of decimal places.
func (d Decimal) StringFixed(places int, decimalSep string) string {
	neg := d.scaled < 0
	val := d.scaled
	if neg {
		val = -val
	}

	intPart := val / Scale
	fracPart := val % Scale

	// Scale is 10000 (4 digits)
	fracStr := fmt.Sprintf("%04d", fracPart)
	if places < 4 {
		// Rounding to target places
		div := int64(1)
		for i := 0; i < 4-places; i++ {
			div *= 10
		}
		roundedFrac := (fracPart + div/2) / div
		maxForPlaces := int64(1)
		for i := 0; i < places; i++ {
			maxForPlaces *= 10
		}
		if roundedFrac >= maxForPlaces {
			intPart++
			roundedFrac -= maxForPlaces
		}
		if places == 0 {
			fracStr = ""
		} else {
			format := fmt.Sprintf("%%0%dd", places)
			fracStr = fmt.Sprintf(format, roundedFrac)
		}
	} else if places > 4 {
		for len(fracStr) < places {
			fracStr += "0"
		}
	}

	prefix := ""
	if neg {
		prefix = "-"
	}

	if places == 0 {
		return fmt.Sprintf("%s%d", prefix, intPart)
	}
	return fmt.Sprintf("%s%d%s%s", prefix, intPart, decimalSep, fracStr)
}

// String returns the decimal formatted with dot notation.
func (d Decimal) String() string {
	return d.StringFixed(2, ".")
}

// MarshalJSON formats Decimal as an exact JSON number: at least 2 decimals, up to the
// 4 of the scale, so 5.40 stays "5.40" and an hourly 0.0074 is not rounded to 0.01 (--json, AD-19).
func (d Decimal) MarshalJSON() ([]byte, error) {
	s := d.StringFixed(4, ".")
	for strings.HasSuffix(s, "0") && len(s)-strings.Index(s, ".") > 3 {
		s = s[:len(s)-1]
	}
	return []byte(s), nil
}

// UnmarshalJSON parses a JSON number or string without using float64.
func (d *Decimal) UnmarshalJSON(b []byte) error {
	trimmed := strings.Trim(string(b), "\"")
	dec, err := ParseDecimal(trimmed)
	if err != nil {
		return err
	}
	*d = dec
	return nil
}

// Pricing represents exact pricing with VAT and monthly/hourly breakdown (AD-6).
type Pricing struct {
	Currency   string  `json:"currency"`    // "EUR", "USD"
	MonthlyHT  Decimal `json:"monthly_ht"`  // Montant mensuel HT
	MonthlyTTC Decimal `json:"monthly_ttc"` // Montant mensuel TTC
	HourlyHT   Decimal `json:"hourly_ht"`   // Montant horaire HT
	HourlyTTC  Decimal `json:"hourly_ttc"`  // Montant horaire TTC
	VATRate    Decimal `json:"vat_rate"`    // Taux de TVA (e.g. 20.00 pour 20%)
	Estimated  bool    `json:"estimated"`   // Marqué "≈" si estimation
}

// CalculatePricing calculates exact monthly and hourly HT and TTC pricing without floats (AD-6).
func CalculatePricing(monthlyHT Decimal, vatRate Decimal, currency string, estimated bool) (*Pricing, error) {
	if monthlyHT.scaled < 0 {
		return nil, ErrNegativeAmount
	}
	if vatRate.scaled < 0 {
		return nil, ErrNegativeVATRate
	}
	if strings.TrimSpace(currency) == "" {
		return nil, ErrEmptyCurrency
	}

	// vatAmount = (monthlyHT * vatRate) / 100
	// In scaled arithmetic (Scale=10000):
	// monthlyHT.scaled is (HT * 10000), vatRate.scaled is (Rate * 10000)
	// vatAmount.scaled = (monthlyHT.scaled * vatRate.scaled + 500000) / (100 * 10000)
	vatAmountScaled := (monthlyHT.scaled*vatRate.scaled + 500000) / 1000000
	rawMonthlyTTCScaled := monthlyHT.scaled + vatAmountScaled

	// Round to cents (2 decimal places):
	ttcCents := (rawMonthlyTTCScaled + 50) / 100
	monthlyTTCScaled := ttcCents * 100

	// Hourly calculation: divide by HoursPerMonth (730)
	hourlyHTScaled := (monthlyHT.scaled + HoursPerMonth/2) / HoursPerMonth
	hourlyTTCScaled := (monthlyTTCScaled + HoursPerMonth/2) / HoursPerMonth

	return &Pricing{
		Currency:   currency,
		MonthlyHT:  monthlyHT,
		MonthlyTTC: NewDecimal(monthlyTTCScaled),
		HourlyHT:   NewDecimal(hourlyHTScaled),
		HourlyTTC:  NewDecimal(hourlyTTCScaled),
		VATRate:    vatRate,
		Estimated:  estimated,
	}, nil
}

// FormatMonthly returns the formatted monthly TTC price (e.g. "5,40 €/mois" or "≈ 5,40 €/mois").
func (p *Pricing) FormatMonthly() string {
	prefix := ""
	if p.Estimated {
		prefix = "≈ "
	}
	sym := currencySymbol(p.Currency)
	valStr := p.MonthlyTTC.StringFixed(2, ",")
	return fmt.Sprintf("%s%s %s/mois", prefix, valStr, sym)
}

// FormatHourly returns the formatted hourly TTC price (e.g. "0,0074 €/h" or "≈ 0,0074 €/h").
func (p *Pricing) FormatHourly() string {
	prefix := ""
	if p.Estimated {
		prefix = "≈ "
	}
	sym := currencySymbol(p.Currency)
	valStr := p.HourlyTTC.StringFixed(4, ",")
	return fmt.Sprintf("%s%s %s/h", prefix, valStr, sym)
}

func currencySymbol(curr string) string {
	switch strings.ToUpper(curr) {
	case "EUR":
		return "€"
	case "USD":
		return "$"
	default:
		return curr
	}
}

// Ensure bytes buffer unused warning doesn't happen
var _ = bytes.Buffer{}
