package health

import (
	"errors"
	"os"
	"testing"
	"time"
)

func loadFixture(t *testing.T) Report {
	t.Helper()
	b, err := os.ReadFile("testdata/health.txt")
	if err != nil {
		t.Fatal(err)
	}
	r, err := Parse(string(b))
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestParseFields(t *testing.T) {
	r := loadFixture(t)

	if got := r.String("app_version"); got != "101.26091.0004" {
		t.Errorf("app_version = %q", got)
	}
	if got := r.String("network_protection_enforcement_level"); got != "block" {
		t.Errorf("[managed] suffix not stripped: %q", got)
	}
	if v, ok := r.Bool("real_time_protection_enabled"); !ok || !v {
		t.Errorf("real_time_protection_enabled = %v, %v", v, ok)
	}
	if v, ok := r.Bool("healthy"); !ok || v {
		t.Errorf("healthy = %v, %v", v, ok)
	}
	if _, ok := r.Bool("app_version"); ok {
		t.Error("a string field must not parse as bool")
	}
	if n, ok := r.Number("definitions_updated_minutes_ago"); !ok || n != 1 {
		t.Errorf("definitions_updated_minutes_ago = %v, %v", n, ok)
	}
}

func TestParseTime(t *testing.T) {
	r := loadFixture(t)
	got, ok := r.Time("product_expiration", time.UTC)
	want := time.Date(2027, time.July, 18, 16, 31, 4, 0, time.UTC)
	if !ok || !got.Equal(want) {
		t.Errorf("product_expiration = %v, %v; want %v", got, ok, want)
	}
}

func TestParseList(t *testing.T) {
	r := loadFixture(t)
	issues, ok := r.List("health_issues")
	if !ok || len(issues) != 1 || issues[0] != "Network Protection cannot start due to unsupported release ring" {
		t.Errorf("health_issues = %q, %v", issues, ok)
	}
	if empty, ok := r.List("conflicting_applications"); !ok || len(empty) != 0 {
		t.Errorf("conflicting_applications = %q, %v", empty, ok)
	}
}

func TestParseSkipsWarnings(t *testing.T) {
	out := "ATTENTION: No license found. Contact your administrator for help.\n" +
		"licensed                                    : false\n"
	r, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Raw) != 1 {
		t.Errorf("expected only the licensed field, got %v", r.Raw)
	}
}

func TestParseDaemonDown(t *testing.T) {
	out := "ATTENTION: Cannot determine license status.\nCould not connect to the daemon\n\tError connecting to server socket\n"
	if _, err := Parse(out); !errors.Is(err, ErrNoFields) {
		t.Errorf("err = %v, want ErrNoFields", err)
	}
}
