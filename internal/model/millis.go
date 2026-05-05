package model

import (
	"database/sql/driver"
	"fmt"
	"strconv"
	"time"
)

// Millis wraps a time.Time and (de)serializes across JSON + SQL as unix
// milliseconds. Zero values marshal to 0 — the DB convention for "unset"
// (columns are `INTEGER NOT NULL DEFAULT 0`).
type Millis struct {
	time.Time
}

// NewMillis builds a Millis from a time.Time.
func NewMillis(t time.Time) Millis { return Millis{t} }

// FromUnixMillis builds a Millis from an int64. 0 yields a zero Millis.
func FromUnixMillis(n int64) Millis {
	if n == 0 {
		return Millis{}
	}
	return Millis{time.UnixMilli(n)}
}

// NowMillis returns the current instant as a Millis.
func NowMillis() Millis { return Millis{time.Now()} }

// UnixMs returns the unix-millis value, 0 for zero.
func (m Millis) UnixMs() int64 {
	if m.Time.IsZero() {
		return 0
	}
	return m.Time.UnixMilli()
}

// MarshalJSON emits an integer. Zero values emit 0.
func (m Millis) MarshalJSON() ([]byte, error) {
	return []byte(strconv.FormatInt(m.UnixMs(), 10)), nil
}

// UnmarshalJSON accepts an integer (unix millis) or null.
func (m *Millis) UnmarshalJSON(b []byte) error {
	s := string(b)
	if s == "null" || s == "" {
		m.Time = time.Time{}
		return nil
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("Millis unmarshal: %w", err)
	}
	if n == 0 {
		m.Time = time.Time{}
	} else {
		m.Time = time.UnixMilli(n)
	}
	return nil
}

// Scan implements sql.Scanner reading INTEGER columns.
func (m *Millis) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		m.Time = time.Time{}
	case int64:
		if v == 0 {
			m.Time = time.Time{}
		} else {
			m.Time = time.UnixMilli(v)
		}
	case []byte:
		return m.UnmarshalJSON(v)
	case string:
		return m.UnmarshalJSON([]byte(v))
	default:
		return fmt.Errorf("Millis Scan: unsupported %T", src)
	}
	return nil
}

// Value implements driver.Valuer producing an INTEGER.
func (m Millis) Value() (driver.Value, error) { return m.UnixMs(), nil }
