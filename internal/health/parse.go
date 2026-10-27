// Package health parses the output of `mdatp health`.
package health

import (
	"bufio"
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Report is the parsed output of `mdatp health`. Raw keeps every field, so
// fields added by future mdatp versions are not lost.
type Report struct {
	Raw map[string]string
}

// ErrNoFields means the output contained no "field : value" lines, which is
// what mdatp prints when it cannot reach its daemon.
var ErrNoFields = errors.New("no health fields found in mdatp output")

// fieldLine matches "app_version      : "101.26091.0004"". Keys are lowercase,
// so warning lines such as "ATTENTION: ..." are skipped.
var fieldLine = regexp.MustCompile(`^([a-z0-9_]+)\s*:\s?(.*)$`)

// dateLayout is how mdatp prints dates, e.g. "Jul 18, 2027 at 04:31:04 PM".
const dateLayout = "Jan 2, 2006 at 3:04:05 PM"

// Parse reads the "field : value" lines printed by `mdatp health`.
func Parse(out string) (Report, error) {
	r := Report{Raw: map[string]string{}}
	sc := bufio.NewScanner(strings.NewReader(out))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		m := fieldLine.FindStringSubmatch(strings.TrimRight(sc.Text(), "\r "))
		if m == nil {
			continue
		}
		r.Raw[m[1]] = clean(m[2])
	}
	if err := sc.Err(); err != nil {
		return r, err
	}
	if len(r.Raw) == 0 {
		return r, ErrNoFields
	}
	return r, nil
}

// clean drops the " [managed]" suffix mdatp adds to policy-controlled fields
// and the quotes around string values.
func clean(v string) string {
	v = strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(v), "[managed]"))
	if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
		v = v[1 : len(v)-1]
	}
	return v
}

// String returns a field as text, or "" when it is missing.
func (r Report) String(field string) string { return r.Raw[field] }

// Bool returns a true/false field. ok is false when the field is missing or
// is not a boolean.
func (r Report) Bool(field string) (value, ok bool) {
	switch r.Raw[field] {
	case "true":
		return true, true
	case "false":
		return false, true
	}
	return false, false
}

// Number returns a numeric field.
func (r Report) Number(field string) (float64, bool) {
	v, err := strconv.ParseFloat(r.Raw[field], 64)
	return v, err == nil
}

// Time returns a date field, interpreted in loc.
func (r Report) Time(field string, loc *time.Location) (time.Time, bool) {
	v := r.Raw[field]
	if v == "" {
		return time.Time{}, false
	}
	t, err := time.ParseInLocation(dateLayout, v, loc)
	return t, err == nil
}

// List returns a JSON array field such as health_issues.
func (r Report) List(field string) ([]string, bool) {
	v := r.Raw[field]
	if v == "" {
		return nil, false
	}
	var out []string
	if err := json.Unmarshal([]byte(v), &out); err != nil {
		return nil, false
	}
	return out, true
}
