// Command mdatp_exporter exposes Microsoft Defender for Endpoint (mdatp) health
// on Linux as Prometheus metrics.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/aunghtetnay/mdatp_exporter/internal/collector"
)

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const landingPage = `<html><head><title>mdatp_exporter</title></head><body>
<h1>mdatp_exporter</h1><p><a href="/metrics">Metrics</a></p></body></html>`

func main() {
	listen := flag.String("web.listen-address", ":10060", "Address to serve metrics on.")
	path := flag.String("mdatp.path", "mdatp", "Path to the mdatp CLI.")
	timeout := flag.Duration("mdatp.timeout", 20*time.Second,
		"How long to wait for `mdatp health` before counting it as hung.")
	ttl := flag.Duration("mdatp.cache-ttl", time.Minute,
		"Reuse the last `mdatp health` result for this long, so frequent scrapes do not load the daemon.")
	backoff := flag.Duration("mdatp.timeout-backoff", 5*time.Minute,
		"After a timeout, wait this long before calling mdatp again.")
	showVersion := flag.Bool("version", false, "Print the version and exit.")
	flag.Parse()

	if *showVersion {
		fmt.Println("mdatp_exporter", version)
		return
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, nil))

	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		collector.New(collector.CommandRunner(*path), collector.Options{
			Timeout:  *timeout,
			CacheTTL: *ttl,
			Backoff:  *backoff,
			Logger:   logger,
		}),
	)

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{
		ErrorLog: slog.NewLogLogger(logger.Handler(), slog.LevelError),
	}))
	mux.HandleFunc("/", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, landingPage)
	})

	srv := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	logger.Info("starting mdatp_exporter", "version", version, "address", *listen, "mdatp", *path)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server stopped", "err", err)
		os.Exit(1)
	}
}
