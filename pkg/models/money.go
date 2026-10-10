// Package models holds the domain types shared by the services, handlers and
// data sources.
package models

import (
	"strconv"
)

// Cents is an amount in the smallest currency unit. Amounts are kept as
// integer cents so that pricing never does float arithmetic, and are written
// to JSON as decimal numbers.
type Cents int64

// ApplyPercent returns percent of c, rounded half up to the nearest cent.
func (c Cents) ApplyPercent(percent int) Cents {
	return (c*Cents(percent) + 50) / 100
}

// Float returns c in whole currency units, e.g. 650 → 6.5.
func (c Cents) Float() float64 {
	return float64(c) / 100
}

// MarshalJSON writes c as a decimal number in whole currency units, using the
// shortest representation, e.g. 650 → 6.5 and 1330 → 13.3.
func (c Cents) MarshalJSON() ([]byte, error) {
	return strconv.AppendFloat(nil, c.Float(), 'f', -1, 64), nil
}
