# mdatp_exporter

Prometheus exporter for **Microsoft Defender for Endpoint on Linux** (`mdatp`).

`systemctl status mdatp` can say `active (running)` while Defender is effectively dead:
`mdatp health` hangs forever, the agent is not onboarded, real-time protection is off,
definitions are weeks old, or the build is about to expire. None of that shows up in a
service check. This exporter turns `mdatp health` into metrics, so Prometheus can alert on it.

```text
mdatp_up 1
mdatp_healthy 0
mdatp_health_issue{issue="Network Protection cannot start due to unsupported release ring"} 1
mdatp_licensed 1
mdatp_real_time_protection_enabled 1
mdatp_definitions_up_to_date 1
mdatp_definitions_age_seconds 60
mdatp_product_expiration_timestamp_seconds 1.815928264e+09
mdatp_health_timeouts_total 0
mdatp_info{app_version="101.26091.0004",engine_version="1.1.25080.4000",network_protection_status="enablement_failed_due_to_edr_capabilities",release_ring="Production"} 1
```

## Metrics

| Metric | Meaning |
|---|---|
| `mdatp_up` | 1 if `mdatp health` answered and was parsed |
| `mdatp_health_timeouts_total` | `mdatp health` calls that hung past the timeout |
| `mdatp_healthy` | Defender's own overall health flag |
| `mdatp_health_issue{issue}` | 1 per entry in `health_issues` |
| `mdatp_licensed` | Device is onboarded and licensed |
| `mdatp_real_time_protection_enabled` | Real-time protection is on |
| `mdatp_definitions_up_to_date` | `definitions_status` is `up_to_date` |
| `mdatp_definitions_age_seconds` | Time since definitions were last updated |
| `mdatp_product_expiration_timestamp_seconds` | When this build expires (builds expire about 9 months after release) |
| `mdatp_info{app_version,engine_version,release_ring,network_protection_status}` | Version labels, always 1 |
| `mdatp_last_check_timestamp_seconds`, `mdatp_check_duration_seconds` | When and how long the last check ran |

Ready-made alerts are in [`examples/alerts.yml`](examples/alerts.yml).

## Gentle on a sick daemon

A hung `mdatp health` call can hold one of the daemon's limited IPC connections, and
every retry makes a stuck daemon worse. So the exporter:

- calls `mdatp health` at most once per `--mdatp.cache-ttl` (default 1m), never concurrently;
- gives up after `--mdatp.timeout` (default 20s) and counts it in `mdatp_health_timeouts_total`;
- after a timeout, waits `--mdatp.timeout-backoff` (default 5m) before trying again.

## Install

```bash
go install github.com/aunghtetnay/mdatp_exporter@latest
sudo cp ~/go/bin/mdatp_exporter /usr/local/bin/
sudo cp examples/mdatp_exporter.service /etc/systemd/system/
sudo systemctl daemon-reload && sudo systemctl enable --now mdatp_exporter
curl -s localhost:10060/metrics | grep ^mdatp_
```

It runs as root because `mdatp health` needs it. It only runs `mdatp health` and changes nothing.

## Flags

| Flag | Default | |
|---|---|---|
| `--web.listen-address` | `:10060` | Where to serve `/metrics` |
| `--mdatp.path` | `mdatp` | Path to the mdatp CLI |
| `--mdatp.timeout` | `20s` | Time before a call counts as hung |
| `--mdatp.cache-ttl` | `1m` | How long a result is reused |
| `--mdatp.timeout-backoff` | `5m` | Wait after a timeout before calling again |

## Status

Early (v0.x), built against the `mdatp health` output of mdatp 101.26xxx. Issues with your
`mdatp health` output (with IDs removed) are very welcome, since field names change between releases.

## License

MIT
