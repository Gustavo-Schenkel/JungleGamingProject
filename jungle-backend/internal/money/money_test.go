package money

import (
	"errors"
	"math"
	"testing"
)

func TestExactParsing(t *testing.T) {
	for _, tt := range []struct {
		input     string
		want      int64
		canonical string
	}{
		{"0", 0, "0.00"}, {"0.00", 0, "0.00"}, {"25", 2500, "25.00"}, {"25.0", 2500, "25.00"},
		{"25.01", 2501, "25.01"}, {"92233720368547758.07", math.MaxInt64, "92233720368547758.07"},
	} {
		m, err := Parse(tt.input, "BRL")
		if err != nil || m.MinorUnits() != tt.want || m.String() != tt.canonical {
			t.Errorf("Parse(%q) = %v %v", tt.input, m, err)
		}
	}
}
func TestInvalidAmounts(t *testing.T) {
	for _, value := range []string{"", "-1", "+1", "01.00", ".50", "1.", "1.001", "1e2", "NaN", "Infinity", " 1.00", "1,00", "92233720368547758.08", "999999999999999999999999999999"} {
		if _, err := Parse(value, "BRL"); err == nil {
			t.Errorf("accepted invalid amount %q", value)
		}
	}
	if _, err := Parse("1.00", "brl"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid currency: %v", err)
	}
}
func TestArithmeticAndMismatch(t *testing.T) {
	a, _ := Parse("100.00", "BRL")
	b, _ := Parse("80.00", "BRL")
	c, err := a.Sub(b)
	if err != nil || c.String() != "20.00" {
		t.Fatalf("subtract: %v %v", c, err)
	}
	d, err := c.Add(b)
	if err != nil || d.String() != "100.00" {
		t.Fatalf("add: %v %v", d, err)
	}
	neg, err := b.Negate()
	if err != nil || neg.String() != "-80.00" {
		t.Fatalf("negate: %v %v", neg, err)
	}
	usd, _ := Parse("1.00", "USD")
	if _, err := a.Add(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("currency mismatch: %v", err)
	}
	if _, err := a.Compare(usd); !errors.Is(err, ErrCurrencyMismatch) {
		t.Fatalf("compare currency mismatch: %v", err)
	}
	max, _ := FromMinorUnits(math.MaxInt64, "BRL")
	if _, err := max.Add(b); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected overflow, got %v", err)
	}
	min, _ := FromMinorUnits(math.MinInt64, "BRL")
	if _, err := min.Negate(); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected negate overflow, got %v", err)
	}
	if _, err := min.Sub(b); !errors.Is(err, ErrOverflow) {
		t.Fatalf("expected subtract overflow, got %v", err)
	}
}
