# Prometheus metrics

Every instance serves the full telemetry set at **`/metrics`** — no flag, no
extra exporter:

```sh
curl http://localhost:8080/metrics
```

```yaml
# prometheus.yml
scrape_configs:
  - job_name: meater
    static_configs:
      - targets: ["meater.local:8080"]
```

Everything the dashboard shows is there, plus the internals behind it:
temperatures in both units (`meater_tip_celsius`, `meater_ambient_fahrenheit`,
…), the fitted rate, the ETA with its range and which model produced it
(`meater_eta_seconds`, `meater_eta_source`), cook identity and progress
(`meater_cook_info`, `meater_cook_progress_ratio`, `meater_cook_duration_seconds`),
link health (`meater_probe_connected`, `meater_last_sample_age_seconds`), what
the database holds (`meater_db_*`), and the standard Go runtime/process
collectors for the service itself. `curl -s localhost:8080/metrics | grep '^# HELP meater'`
lists the lot with descriptions.

Two conventions worth knowing when writing queries:

- **Unknown is `NaN`, not `-1`.** The JSON API reports "no ETA" as `-1`, which
  would graph and alert as a real duration. The exporter emits `NaN` so
  Prometheus skips it instead.
- **Enumerations are one series per value**, not a numeric code — so
  `meater_state{state="ready"} == 1` is a complete alert rule, and a state that
  has not happened yet still produces a series rather than a gap.

```yaml
# Shout when it's done, and notice a wedged BLE link.
groups:
  - name: meater
    rules:
      - alert: MeaterReady
        expr: meater_state{state="ready"} == 1
      - alert: MeaterProbeSilent
        expr: meater_discovery_running == 1 and meater_last_sample_age_seconds > 300
```
