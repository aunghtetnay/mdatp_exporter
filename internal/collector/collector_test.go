package collector

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

type fakeClock struct{ t time.Time }

func (f *fakeClock) Now() time.Time          { return f.t }
func (f *fakeClock) Advance(d time.Duration) { f.t = f.t.Add(d) }

type fakeRunner struct {
	out   string
	err   error
	calls int
}

func (f *fakeRunner) Run(context.Context) (string, error) {
	f.calls++
	return f.out, f.err
}

func newTest(t *testing.T, run *fakeRunner) (*Collector, *fakeClock, *prometheus.Registry) {
	t.Helper()
	clock := &fakeClock{t: time.Date(2026, time.October, 26, 21, 0, 0, 0, time.UTC)}
	c := New(run.Run, Options{Location: time.UTC, Now: clock.Now})
	reg := prometheus.NewPedanticRegistry()
	reg.MustRegister(c)
	return c, clock, reg
}

// gather returns every sample as "name{label="value",...}" -> value.
func gather(t *testing.T, reg *prometheus.Registry) map[string]float64 {
	t.Helper()
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]float64{}
	for _, f := range families {
		for _, m := range f.GetMetric() {
			var labels []string
			for _, l := range m.GetLabel() {
				labels = append(labels, fmt.Sprintf("%s=%q", l.GetName(), l.GetValue()))
			}
			sort.Strings(labels)
			key := f.GetName()
			if len(labels) > 0 {
				key += "{" + strings.Join(labels, ",") + "}"
			}
			switch {
			case m.GetGauge() != nil:
				out[key] = m.GetGauge().GetValue()
			case m.GetCounter() != nil:
				out[key] = m.GetCounter().GetValue()
			}
		}
	}
	return out
}

func fixture(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile("../health/testdata/health.txt")
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestHealthyOutput(t *testing.T) {
	_, _, reg := newTest(t, &fakeRunner{out: fixture(t)})
	got := gather(t, reg)

	want := map[string]float64{
		"mdatp_up":                                   1,
		"mdatp_healthy":                              0,
		"mdatp_licensed":                             1,
		"mdatp_real_time_protection_enabled":         1,
		"mdatp_definitions_up_to_date":               1,
		"mdatp_definitions_age_seconds":              60,
		"mdatp_health_timeouts_total":                0,
		"mdatp_product_expiration_timestamp_seconds": float64(time.Date(2027, time.July, 18, 16, 31, 4, 0, time.UTC).Unix()),
		`mdatp_health_issue{issue="Network Protection cannot start due to unsupported release ring"}`:                                                                              1,
		`mdatp_info{app_version="101.26091.0004",engine_version="1.1.25080.4000",network_protection_status="enablement_failed_due_to_edr_capabilities",release_ring="Production"}`: 1,
	}
	for k, v := range want {
		if g, ok := got[k]; !ok || g != v {
			t.Errorf("%s = %v (present %v), want %v", k, g, ok, v)
		}
	}
}

func TestCachesResults(t *testing.T) {
	run := &fakeRunner{out: fixture(t)}
	_, clock, reg := newTest(t, run)

	gather(t, reg)
	clock.Advance(30 * time.Second)
	gather(t, reg)
	if run.calls != 1 {
		t.Fatalf("mdatp called %d times within the cache TTL, want 1", run.calls)
	}
	clock.Advance(31 * time.Second)
	gather(t, reg)
	if run.calls != 2 {
		t.Fatalf("mdatp called %d times after the TTL, want 2", run.calls)
	}
}

func TestTimeoutBacksOff(t *testing.T) {
	run := &fakeRunner{err: ErrTimeout}
	_, clock, reg := newTest(t, run)

	got := gather(t, reg)
	if got["mdatp_up"] != 0 || got["mdatp_health_timeouts_total"] != 1 {
		t.Fatalf("after timeout: up=%v timeouts=%v", got["mdatp_up"], got["mdatp_health_timeouts_total"])
	}

	// Past the normal TTL but inside the backoff: must not call the hung daemon again.
	clock.Advance(2 * time.Minute)
	gather(t, reg)
	if run.calls != 1 {
		t.Fatalf("mdatp called %d times during backoff, want 1", run.calls)
	}

	clock.Advance(4 * time.Minute)
	run.err, run.out = nil, fixture(t)
	if got := gather(t, reg); got["mdatp_up"] != 1 {
		t.Fatalf("after backoff: up=%v, want 1", got["mdatp_up"])
	}
}

func TestDaemonDown(t *testing.T) {
	run := &fakeRunner{err: errors.New("exit status 1: Could not connect to the daemon")}
	_, _, reg := newTest(t, run)
	got := gather(t, reg)
	if got["mdatp_up"] != 0 {
		t.Errorf("mdatp_up = %v, want 0", got["mdatp_up"])
	}
	if _, ok := got["mdatp_healthy"]; ok {
		t.Error("mdatp_healthy must not be exported when mdatp did not answer")
	}
}
