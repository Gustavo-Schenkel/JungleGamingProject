// Package money provides exact two-decimal monetary arithmetic without floats.
package money

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
)

var (
	ErrInvalid          = errors.New("invalid monetary amount or currency")
	ErrOverflow         = errors.New("monetary arithmetic overflow")
	ErrCurrencyMismatch = errors.New("currencies do not match")
)

var decimalPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)(?:\.([0-9]{1,2}))?$`)
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// Money holds a signed number of minor units (cents) and an ISO currency code.
// External inputs are nonnegative; signed differences are used only internally.
type Money struct {
	minor    int64
	currency string
}

// Parse accepts exact, nonnegative amounts such as 0, 1.5 and 100.00.
// It rejects exponent notation, negative signs, excess fractional digits and overflow.
func Parse(amount, currency string) (Money, error) {
	if !currencyPattern.MatchString(currency) {
		return Money{}, ErrInvalid
	}
	parts := decimalPattern.FindStringSubmatch(amount)
	if parts == nil {
		return Money{}, ErrInvalid
	}
	whole, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil {
		return Money{}, ErrOverflow
	}
	fracString := parts[2]
	var fraction int64
	if len(fracString) > 0 {
		if len(fracString) == 1 {
			fracString += "0"
		}
		fraction, err = strconv.ParseInt(fracString, 10, 64)
		if err != nil {
			return Money{}, ErrInvalid
		}
	}
	if whole > (math.MaxInt64-fraction)/100 {
		return Money{}, ErrOverflow
	}
	return Money{minor: whole*100 + fraction, currency: currency}, nil
}

func Zero(currency string) (Money, error) { return Parse("0.00", currency) }

// FromMinorUnits rehydrates a Money value from trusted database data.
func FromMinorUnits(minor int64, currency string) (Money, error) {
	if !currencyPattern.MatchString(currency) {
		return Money{}, ErrInvalid
	}
	return Money{minor: minor, currency: currency}, nil
}

func (m Money) MinorUnits() int64 { return m.minor }
func (m Money) Currency() string  { return m.currency }

func (m Money) valid() bool { return currencyPattern.MatchString(m.currency) }
func (m Money) compatible(n Money) error {
	if !m.valid() || !n.valid() {
		return ErrInvalid
	}
	if m.currency != n.currency {
		return ErrCurrencyMismatch
	}
	return nil
}

func (m Money) Add(n Money) (Money, error) {
	if err := m.compatible(n); err != nil {
		return Money{}, err
	}
	if (n.minor > 0 && m.minor > math.MaxInt64-n.minor) ||
		(n.minor < 0 && m.minor < math.MinInt64-n.minor) {
		return Money{}, ErrOverflow
	}
	return Money{minor: m.minor + n.minor, currency: m.currency}, nil
}
func (m Money) Negate() (Money, error) {
	if !m.valid() {
		return Money{}, ErrInvalid
	}
	if m.minor == math.MinInt64 {
		return Money{}, ErrOverflow
	}
	return Money{minor: -m.minor, currency: m.currency}, nil
}
func (m Money) Sub(n Money) (Money, error) {
	if err := m.compatible(n); err != nil {
		return Money{}, err
	}
	if (n.minor < 0 && m.minor > math.MaxInt64+n.minor) ||
		(n.minor > 0 && m.minor < math.MinInt64+n.minor) {
		return Money{}, ErrOverflow
	}
	return Money{minor: m.minor - n.minor, currency: m.currency}, nil
}
func (m Money) Compare(n Money) (int, error) {
	if err := m.compatible(n); err != nil {
		return 0, err
	}
	switch {
	case m.minor < n.minor:
		return -1, nil
	case m.minor > n.minor:
		return 1, nil
	default:
		return 0, nil
	}
}
func (m Money) String() string {
	if !m.valid() {
		return "<invalid>"
	}
	// Avoid negating MinInt64: -(n+1) is always safe.
	if m.minor < 0 {
		abs := uint64(-(m.minor + 1)) + 1
		return fmt.Sprintf("-%d.%02d", abs/100, abs%100)
	}
	return fmt.Sprintf("%d.%02d", m.minor/100, m.minor%100)
}

// JSONMoney is the external contract; amount stays a JSON string.
type JSONMoney struct {
	Amount   string `json:"amount"`
	Currency string `json:"currency"`
}

func (j JSONMoney) Parse() (Money, error) { return Parse(j.Amount, j.Currency) }
func (m Money) JSON() JSONMoney           { return JSONMoney{Amount: m.String(), Currency: m.currency} }

// IsCanonicalCurrency is used by input validation outside the money package.
func IsCanonicalCurrency(c string) bool {
	return currencyPattern.MatchString(c) && strings.TrimSpace(c) == c
}
