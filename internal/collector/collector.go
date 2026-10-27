// Package collector exposes `mdatp health` as Prometheus metrics.
package collector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/aunghtetnay/mdatp_exporter/internal/health"
)

// ErrTimeout is returned by a Runner when mdatp did not answer in time.
var ErrTimeout = errors.New("mdatp health timed out")

// Runner returns the output of `mdatp health`.
type Runner func(ctx context.Context) (string, error)

// CommandRunner runs `<path> health`.
func CommandRunner(path string) Runner {
	return func(ctx context.Context) (string, error) {
		out, err := exec.CommandContext(ctx, path, "health").CombinedOutput()
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return "", ErrTimeout
		}
		if err != nil {
			msg := strings.TrimSpace(string(out))
			if msg == "" {
				return "", err
			}
			if len(msg) > 200 {
				msg = msg[:200]
			}
			return "", fmt.Errorf("%w: %s", err, msg)
		}
		return string(out), nil
	}
}

// Options tune how often mdatp is called.
type Options struct {
	// Timeout is how long one `mdatp health` call may take.
	Timeout time.Duration
	// CacheTTL is how long a result is reused, so frequent scrapes do not load the daemon.
	CacheTTL time.Duration
	// Backoff is how long to wait after a timeout before calling mdatp again.
	// A hung call can hold one of the daemon's limited IPC connections, so
	// retrying on every scrape would make a stuck daemon worse.
	Backoff time.Duration
	// Location is used to read the dates mdatp prints in local time.
	Location *time.Location
	Logger   *slog.Logger
	Now      func() time.Time
}

// Collector is a prometheus.Collector for mdatp health.
type Collector struct {
	run  Runner
	opts Options

	mu       sync.Mutex
	last     snapshot
	timeouts float64
}

type snapshot struct {
	at       time.Time
	duration time.Duration
	report   health.Report
	ok       bool
	timedOut bool
}

// New returns a Collector. Zero options get sensible defaults.
func New(run Runner, opts Options) *Collector {
	if opts.Timeout == 0 {
		opts.Timeout = 20 * time.Second
	}
	if opts.CacheTTL == 0 {
		opts.CacheTTL = time.Minute
	}
	if opts.Backoff == 0 {
		opts.Backoff = 5 * time.Minute
	}
	if opts.Location == nil {
		opts.Location = time.Local
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Collector{run: run, opts: opts}
}

func desc(name, help string, labels ...string) *prometheus.Desc {
	return prometheus.NewDesc(prometheus.BuildFQName("mdatp", "", name), help, labels, nil)
}

var (
	upDesc         = desc("up", "1 if `mdatp health` answered and was parsed, 0 otherwise.")
	timeoutsDesc   = desc("health_timeouts_total", "Calls to `mdatp health` that did not answer within the timeout (a hung daemon).")
	lastCheckDesc  = desc("last_check_timestamp_seconds", "When `mdatp health` was last run.")
	durationDesc   = desc("check_duration_seconds", "How long the last `mdatp health` call took.")
	healthyDesc    = desc("healthy", "1 if mdatp reports itself healthy.")
	issueDesc      = desc("health_issue", "1 for every issue mdatp lists in health_issues.", "issue")
	licensedDesc   = desc("licensed", "1 if the device is onboarded and licensed.")
	rtpDesc        = desc("real_time_protection_enabled", "1 if real-time protection is on.")
	defsOKDesc     = desc("definitions_up_to_date", "1 if definitions_status is up_to_date.")
	defsAgeDesc    = desc("definitions_age_seconds", "Seconds since the security intelligence definitions were updated.")
	expirationDesc = desc("product_expiration_timestamp_seconds", "When this mdatp build expires and must be upgraded.")
	infoDesc       = desc("info", "Version and configuration labels, always 1.",
		"app_version", "engine_version", "release_ring", "network_protection_status")
)

var allDescs = []*prometheus.Desc{
	upDesc, timeoutsDesc, lastCheckDesc, durationDesc, healthyDesc, issueDesc, licensedDesc,
	rtpDesc, defsOKDesc, defsAgeDesc, expirationDesc, infoDesc,
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, d := range allDescs {
		ch <- d
	}
}

// Collect implements prometheus.Collector. Scrapes are serialized, so mdatp
// is never called twice at the same time.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refresh()

	s := c.last
	ch <- gauge(upDesc, boolValue(s.ok))
	ch <- prometheus.MustNewConstMetric(timeoutsDesc, prometheus.CounterValue, c.timeouts)
	if !s.at.IsZero() {
		ch <- gauge(lastCheckDesc, float64(s.at.Unix()))
		ch <- gauge(durationDesc, s.duration.Seconds())
	}
	if !s.ok {
		return
	}

	r := s.report
	emitBool(ch, healthyDesc, r, "healthy")
	emitBool(ch, licensedDesc, r, "licensed")
	emitBool(ch, rtpDesc, r, "real_time_protection_enabled")

	if issues, ok := r.List("health_issues"); ok {
		seen := map[string]bool{}
		for _, issue := range issues {
			if !seen[issue] {
				seen[issue] = true
				ch <- gauge(issueDesc, 1, issue)
			}
		}
	}

	if status := r.String("definitions_status"); status != "" {
		ch <- gauge(defsOKDesc, boolValue(status == "up_to_date"))
	}
	if minutes, ok := r.Number("definitions_updated_minutes_ago"); ok {
		ch <- gauge(defsAgeDesc, minutes*60)
	} else if updated, ok := r.Time("definitions_updated", c.opts.Location); ok {
		ch <- gauge(defsAgeDesc, c.opts.Now().Sub(updated).Seconds())
	}
	if exp, ok := r.Time("product_expiration", c.opts.Location); ok {
		ch <- gauge(expirationDesc, float64(exp.Unix()))
	}

	ch <- gauge(infoDesc, 1, r.String("app_version"), r.String("engine_version"),
		r.String("release_ring"), r.String("network_protection_status"))
}

// refresh calls mdatp when the cached result is stale.
func (c *Collector) refresh() {
	wait := c.opts.CacheTTL
	if c.last.timedOut {
		wait = c.opts.Backoff
	}
	if !c.last.at.IsZero() && c.opts.Now().Sub(c.last.at) < wait {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), c.opts.Timeout)
	defer cancel()
	start := c.opts.Now()
	out, err := c.run(ctx)
	s := snapshot{at: start, duration: c.opts.Now().Sub(start)}

	switch {
	case errors.Is(err, ErrTimeout):
		s.timedOut = true
		c.timeouts++
		c.opts.Logger.Warn("mdatp health timed out; backing off", "timeout", c.opts.Timeout, "backoff", c.opts.Backoff)
	case err != nil:
		c.opts.Logger.Warn("mdatp health failed", "err", err)
	default:
		report, perr := health.Parse(out)
		if perr != nil {
			c.opts.Logger.Warn("could not parse mdatp health output", "err", perr)
			break
		}
		s.report = report
		s.ok = true
	}
	c.last = s
}

func gauge(d *prometheus.Desc, v float64, labels ...string) prometheus.Metric {
	return prometheus.MustNewConstMetric(d, prometheus.GaugeValue, v, labels...)
}

func emitBool(ch chan<- prometheus.Metric, d *prometheus.Desc, r health.Report, field string) {
	if v, ok := r.Bool(field); ok {
		ch <- gauge(d, boolValue(v))
	}
}

func boolValue(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
