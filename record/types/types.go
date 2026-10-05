// SPDX-License-Identifier: MIT
// Copyright (c) 2026 OgonFrameworks. All rights reserved.
//
// Custom scalar types used by OgonRecord models. These provide driver-agnostic
// SQL/JSON serialization so that the same model struct works for both
// Postgres (pgx) and SQLite (modernc.org/sqlite). All types are value types
// (or trivial wrappers) so they can be embedded in user model structs
// without taking the address of a method receiver.

package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
)

// UUID is a 128-bit identifier backed by google/uuid. It scans from
// Postgres uuid and SQLite text; it serialises to a canonical hyphenated
// string. The zero value is treated as the all-zeros UUID.
type UUID struct {
	uuid.UUID
}

// NewUUIDv4 returns a fresh random UUIDv4.
func NewUUIDv4() UUID { return UUID{UUID: uuid.New()} }

// ParseUUID parses a canonical UUID string. It returns an error on malformed
// input — callers MUST surface the error as a diagnostic rather than panic.
func ParseUUID(s string) (UUID, error) {
	u, err := uuid.Parse(s)
	if err != nil {
		return UUID{}, fmt.Errorf("types: bad uuid %q: %w", s, err)
	}
	return UUID{UUID: u}, nil
}

// Scan implements sql.Scanner for both pgx and database/sql paths.
func (u *UUID) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		u.UUID = uuid.Nil
		return nil
	case uuid.UUID:
		u.UUID = v
		return nil
	case *uuid.UUID:
		u.UUID = *v
		return nil
	case []byte:
		id, err := uuid.ParseBytes(v)
		if err != nil {
			return err
		}
		u.UUID = id
		return nil
	case string:
		id, err := uuid.Parse(v)
		if err != nil {
			return err
		}
		u.UUID = id
		return nil
	}
	return fmt.Errorf("types: cannot scan %T into UUID", src)
}

// Value implements driver.Valuer so UUID serialises as a canonical string.
func (u UUID) Value() (driver.Value, error) {
	if u.UUID == uuid.Nil {
		return nil, nil
	}
	return u.UUID.String(), nil
}

// String returns the canonical hyphenated form.
func (u UUID) String() string { return u.UUID.String() }

// IsZero reports whether the UUID is all zeros (the model's zero value).
func (u UUID) IsZero() bool { return u.UUID == uuid.Nil }

// MarshalJSON renders the UUID as a JSON string. The zero value renders
// as an empty string to keep REST payloads tidy.
func (u UUID) MarshalJSON() ([]byte, error) {
	if u.UUID == uuid.Nil {
		return []byte(`""`), nil
	}
	return json.Marshal(u.UUID.String())
}

// UnmarshalJSON accepts both canonical and braced forms.
func (u *UUID) UnmarshalJSON(p []byte) error {
	var s string
	if err := json.Unmarshal(p, &s); err != nil {
		return err
	}
	if s == "" {
		u.UUID = uuid.Nil
		return nil
	}
	id, err := uuid.Parse(s)
	if err != nil {
		return err
	}
	u.UUID = id
	return nil
}

// Decimal is a fixed-point decimal backed by shopspring/decimal. It scans
// from Postgres numeric/decimal and SQLite real/text. It serialises to
// JSON as a string to preserve scale (DATA-027).
type Decimal struct {
	decimal.Decimal
}

// NewDecimal constructs a Decimal from common numeric literal types.
// It accepts int, int64, float64, and string. Other types return an error.
func NewDecimal(v any) (Decimal, error) {
	switch x := v.(type) {
	case int:
		return Decimal{Decimal: decimal.NewFromInt(int64(x))}, nil
	case int32:
		return Decimal{Decimal: decimal.NewFromInt32(x)}, nil
	case int64:
		return Decimal{Decimal: decimal.NewFromInt(x)}, nil
	case uint64:
		return Decimal{Decimal: decimal.NewFromUint64(x)}, nil
	case float32:
		return Decimal{Decimal: decimal.NewFromFloat32(x)}, nil
	case float64:
		return Decimal{Decimal: decimal.NewFromFloat(x)}, nil
	case string:
		d, err := decimal.NewFromString(x)
		if err != nil {
			return Decimal{}, err
		}
		return Decimal{Decimal: d}, nil
	}
	return Decimal{}, fmt.Errorf("types: NewDecimal does not accept %T", v)
}

// Scan implements sql.Scanner.
func (d *Decimal) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		d.Decimal = decimal.Decimal{}
		return nil
	case float64:
		d.Decimal = decimal.NewFromFloat(v)
		return nil
	case float32:
		d.Decimal = decimal.NewFromFloat(float64(v))
		return nil
	case int64:
		d.Decimal = decimal.NewFromInt(v)
		return nil
	case int:
		d.Decimal = decimal.NewFromInt(int64(v))
		return nil
	case []byte:
		dec, err := decimal.NewFromString(string(v))
		if err != nil {
			return err
		}
		d.Decimal = dec
		return nil
	case string:
		dec, err := decimal.NewFromString(v)
		if err != nil {
			return err
		}
		d.Decimal = dec
		return nil
	}
	return fmt.Errorf("types: cannot scan %T into Decimal", src)
}

// Value implements driver.Valuer.
func (d Decimal) Value() (driver.Value, error) {
	if d.Decimal.IsZero() {
		return nil, nil
	}
	return d.Decimal.String(), nil
}

// MarshalJSON serialises as a string to preserve scale.
func (d Decimal) MarshalJSON() ([]byte, error) {
	return json.Marshal(d.Decimal.String())
}

// UnmarshalJSON accepts a JSON string or number.
func (d *Decimal) UnmarshalJSON(p []byte) error {
	var s string
	if err := json.Unmarshal(p, &s); err == nil && s != "" {
		dec, err := decimal.NewFromString(s)
		if err != nil {
			return err
		}
		d.Decimal = dec
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(p, &n); err != nil {
		return err
	}
	dec, err := decimal.NewFromString(n.String())
	if err != nil {
		return err
	}
	d.Decimal = dec
	return nil
}

// JSONB is a JSON/JSONB column. It stores the raw encoded bytes and lets
// callers (Un)marshal into domain types. nil bytes serialise as SQL NULL.
type JSONB []byte

// MarshalJSON returns the underlying bytes verbatim — the field is already
// encoded JSON. Empty JSONB renders as JSON null.
func (j JSONB) MarshalJSON() ([]byte, error) {
	if len(j) == 0 {
		return []byte("null"), nil
	}
	return j, nil
}

// UnmarshalJSON copies the supplied bytes.
func (j *JSONB) UnmarshalJSON(p []byte) error {
	if j == nil {
		return errors.New("types: UnmarshalJSON on nil *JSONB")
	}
	buf := make([]byte, len(p))
	copy(buf, p)
	*j = JSONB(buf)
	return nil
}

// Scan accepts []byte, string, or nil from any driver.
func (j *JSONB) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*j = nil
		return nil
	case []byte:
		buf := make([]byte, len(v))
		copy(buf, v)
		*j = JSONB(buf)
		return nil
	case string:
		*j = JSONB(v)
		return nil
	}
	return fmt.Errorf("types: cannot scan %T into JSONB", src)
}

// Value implements driver.Valuer.
func (j JSONB) Value() (driver.Value, error) {
	if len(j) == 0 {
		return nil, nil
	}
	return []byte(j), nil
}

// As decodes the JSONB into a typed value. The destination MUST be a pointer
// or json.Unmarshal will reject it.
func (j JSONB) As(v any) error {
	if len(j) == 0 {
		return nil
	}
	return json.Unmarshal(j, v)
}

// From encodes the supplied value into the JSONB column.
func (j *JSONB) From(v any) error {
	p, err := json.Marshal(v)
	if err != nil {
		return err
	}
	*j = JSONB(p)
	return nil
}

// Time is a thin alias over time.Time that lets scanners detect the
// OgonRecord convention (UTC storage, microsecond precision for Postgres,
// millisecond for SQLite).
type Time = time.Time

// Enum constrains user-defined enum types. An enum type implements Enum so
// the registry can validate member values and the migration engine can emit
// the appropriate Postgres CREATE TYPE ... AS ENUM or SQLite CHECK clause
// (DATA-029).
type Enum interface {
	~string | ~int
	// Values returns the ordered set of enum members. The order is normative
	// for Postgres enum type emission (DATA-097).
	Values() []string
	// Label returns the canonical string for the current value.
	Label() string
	// IsValid reports whether the current value is a member of Values().
	IsValid() bool
}
