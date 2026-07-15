// Package metrics exposes the probe monitor to Prometheus.
//
// Everything the web UI shows is published here, plus the internals behind it:
// the raw temperatures in both units, the fitted rise rate, the ETA with its
// confidence range and which model produced it, the cook session's identity and
// progress, and what the database holds. The Go runtime and process collectors
// are registered alongside, so a single scrape of /metrics also covers the
// service's own health.
//
// Two conventions are worth knowing when writing queries against this:
//
//   - Unknown values are NaN, not a sentinel. The monitor reports "no ETA" as
//     -1, which would graph and alert as if it were a real duration; NaN makes
//     the gap explicit and Prometheus skips it in aggregations.
//   - Enumerations (state, ETA source) are published as one series per possible
//     value with a 0/1 body, not as a numeric code. That way `meater_state{state="ready"} == 1`
//     is a complete alert rule, and a state that has never occurred still
//     produces a series rather than a gap.
package metrics

import (
	"log"
	"math"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/awlx/meater-golang/internal/monitor"
	"github.com/awlx/meater-golang/internal/store"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// storeStatsTTL is how long database counts are reused between scrapes.
// Counting samples is a full table scan over a long cook, and the store holds a
// single connection shared with the writer, so a scrape must not run it every
// time. The counts move slowly enough that half a minute of staleness is
// invisible.
const storeStatsTTL = 30 * time.Second

// allStates is every state the monitor can report. The collector emits a series
// for each on every scrape so a dashboard has no gaps for states that have not
// happened yet.
var allStates = []string{
	monitor.StateIdle,
	monitor.StateDisconnected,
	monitor.StateWaiting,
	monitor.StateCooking,
	monitor.StateStalled,
	monitor.StateReady,
}

// allETASources is every model that can produce the time-to-target estimate.
// "none" covers a cook with no estimate at all, so the series always sum to 1.
var allETASources = []string{"physics", "history", "blend", "none"}

// Collector publishes the monitor (and optionally the store) as Prometheus
// metrics. It reads state on scrape rather than mirroring it into gauges, so
// the numbers can never drift from what the API reports.
type Collector struct {
	mon *monitor.Monitor
	st  *store.Store

	// Cached database counts; see storeStatsTTL.
	mu          sync.Mutex
	storeStats  store.Stats
	storeStatOK bool
	storeStatAt time.Time

	up           *prometheus.Desc
	buildInfo    *prometheus.Desc
	connected    *prometheus.Desc
	running      *prometheus.Desc
	hasReading   *prometheus.Desc
	state        *prometheus.Desc
	tipC         *prometheus.Desc
	tipF         *prometheus.Desc
	ambientC     *prometheus.Desc
	ambientF     *prometheus.Desc
	ambientAvgC  *prometheus.Desc
	targetC      *prometheus.Desc
	targetF      *prometheus.Desc
	rate         *prometheus.Desc
	etaSeconds   *prometheus.Desc
	etaLow       *prometheus.Desc
	etaHigh      *prometheus.Desc
	etaSamples   *prometheus.Desc
	etaSource    *prometheus.Desc
	readyAt      *prometheus.Desc
	progress     *prometheus.Desc
	remainingC   *prometheus.Desc
	cookInfo     *prometheus.Desc
	cookID       *prometheus.Desc
	cookStart    *prometheus.Desc
	cookDuration *prometheus.Desc
	cookSamples  *prometheus.Desc
	cookStartTip *prometheus.Desc
	cookMaxTip   *prometheus.Desc
	cookMaxAmb   *prometheus.Desc
	updatedAt    *prometheus.Desc
	lastSample   *prometheus.Desc
	sampleAge    *prometheus.Desc
	histCooks    *prometheus.Desc
	dbCooks      *prometheus.Desc
	dbFinished   *prometheus.Desc
	dbSamples    *prometheus.Desc
	dbUp         *prometheus.Desc
}

// NewCollector builds a Collector over the monitor. st may be nil, which drops
// the meater_db_* metrics.
func NewCollector(mon *monitor.Monitor, st *store.Store) *Collector {
	d := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc(name, help, labels, nil)
	}
	return &Collector{
		mon: mon,
		st:  st,

		up: d("meater_up",
			"Always 1; present so a scrape of a running exporter is distinguishable from a down target."),
		buildInfo: d("meater_build_info",
			"Build metadata for the running binary, as labels with a constant value of 1.",
			"version", "goversion"),

		connected: d("meater_probe_connected",
			"1 when the BLE link to the probe is up, 0 otherwise."),
		running: d("meater_discovery_running",
			"1 when probe discovery is active (a cook session has been started), 0 when stopped."),
		hasReading: d("meater_probe_has_reading",
			"1 once a reading has arrived on the current connection, 0 while still waiting."),
		state: d("meater_state",
			"Current cook state, one series per possible state with a value of 1 for the active one.",
			"state"),

		tipC: d("meater_tip_celsius",
			"Internal (meat) temperature at the probe tip, in degrees Celsius."),
		tipF: d("meater_tip_fahrenheit",
			"Internal (meat) temperature at the probe tip, in degrees Fahrenheit."),
		ambientC: d("meater_ambient_celsius",
			"Ambient (cook chamber) temperature, in degrees Celsius."),
		ambientF: d("meater_ambient_fahrenheit",
			"Ambient (cook chamber) temperature, in degrees Fahrenheit."),
		ambientAvgC: d("meater_ambient_average_celsius",
			"Ambient temperature smoothed over the recent window, as fed to the ETA model, in degrees Celsius."),
		targetC: d("meater_target_celsius",
			"Target tip temperature, in degrees Celsius."),
		targetF: d("meater_target_fahrenheit",
			"Target tip temperature, in degrees Fahrenheit."),

		rate: d("meater_rate_celsius_per_minute",
			"Tip temperature rise rate from a least-squares fit over the recent window, in degrees Celsius per minute."),
		etaSeconds: d("meater_eta_seconds",
			"Estimated seconds until the tip reaches the target; 0 when already there, NaN when unknown (a deep stall)."),
		etaLow: d("meater_eta_low_seconds",
			"Lower bound of the estimate's range, in seconds; NaN when no range is available."),
		etaHigh: d("meater_eta_high_seconds",
			"Upper bound of the estimate's range, in seconds; NaN when no range is available."),
		etaSamples: d("meater_eta_history_samples",
			"Number of comparable past cooks informing the current estimate."),
		etaSource: d("meater_eta_source",
			"Which model produced the estimate, one series per source with a value of 1 for the active one.",
			"source"),
		readyAt: d("meater_ready_timestamp_seconds",
			"Unix time at which the tip is estimated to reach the target; NaN when unknown."),
		progress: d("meater_cook_progress_ratio",
			"Fraction of the climb from the cook's starting tip temperature to the target that is complete, 0..1; NaN when not measurable."),
		remainingC: d("meater_remaining_celsius",
			"Degrees Celsius the tip is still short of the target; 0 once reached."),

		cookInfo: d("meater_cook_info",
			"Identity of the current cook, as labels with a constant value of 1.",
			"cook_id", "name", "meat_type"),
		cookID: d("meater_cook_id",
			"Database id of the current cook; 0 when no cook is open."),
		cookStart: d("meater_cook_started_timestamp_seconds",
			"Unix time of the current cook's first reading; NaN when no cook is open."),
		cookDuration: d("meater_cook_duration_seconds",
			"Seconds elapsed since the current cook's first reading; NaN when no cook is open."),
		cookSamples: d("meater_cook_samples",
			"Readings retained in memory for the current cook."),
		cookStartTip: d("meater_cook_start_tip_celsius",
			"Tip temperature the current cook began at, in degrees Celsius; NaN when no cook is open."),
		cookMaxTip: d("meater_cook_max_tip_celsius",
			"Highest tip temperature seen during the current cook, in degrees Celsius; NaN when no cook is open."),
		cookMaxAmb: d("meater_cook_max_ambient_celsius",
			"Highest ambient temperature seen during the current cook, in degrees Celsius; NaN when no cook is open."),

		updatedAt: d("meater_last_update_timestamp_seconds",
			"Unix time the monitor last published a status; NaN before the first one."),
		lastSample: d("meater_last_sample_timestamp_seconds",
			"Unix time of the most recent probe reading; NaN before the first one."),
		sampleAge: d("meater_last_sample_age_seconds",
			"Seconds since the most recent probe reading; NaN before the first one. Alert on this to catch a silently wedged BLE link."),
		histCooks: d("meater_history_model_cooks",
			"Past cooks the learned time-to-target model currently draws on."),

		dbUp: d("meater_db_up",
			"1 when the last attempt to read database counts succeeded, 0 when it failed."),
		dbCooks: d("meater_db_cooks",
			"Cooks retained in the database, including the open one."),
		dbFinished: d("meater_db_finished_cooks",
			"Finished cooks retained in the database."),
		dbSamples: d("meater_db_samples",
			"Temperature samples retained in the database across all cooks."),
	}
}

// Describe implements prometheus.Collector.
func (c *Collector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range []*prometheus.Desc{
		c.up, c.buildInfo, c.connected, c.running, c.hasReading, c.state,
		c.tipC, c.tipF, c.ambientC, c.ambientF, c.ambientAvgC, c.targetC, c.targetF,
		c.rate, c.etaSeconds, c.etaLow, c.etaHigh, c.etaSamples, c.etaSource,
		c.readyAt, c.progress, c.remainingC,
		c.cookInfo, c.cookID, c.cookStart, c.cookDuration, c.cookSamples,
		c.cookStartTip, c.cookMaxTip, c.cookMaxAmb,
		c.updatedAt, c.lastSample, c.sampleAge, c.histCooks,
		c.dbUp, c.dbCooks, c.dbFinished, c.dbSamples,
	} {
		ch <- desc
	}
}

// Collect implements prometheus.Collector.
func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	m := c.mon.Metrics()
	s := m.Status
	now := time.Now()

	gauge := func(desc *prometheus.Desc, v float64, labels ...string) {
		ch <- prometheus.MustNewConstMetric(desc, prometheus.GaugeValue, v, labels...)
	}

	gauge(c.up, 1)
	gauge(c.buildInfo, 1, version(), goVersion())

	gauge(c.connected, b2f(s.Connected))
	gauge(c.running, b2f(s.Running))
	gauge(c.hasReading, b2f(s.HasReading))
	for _, st := range allStates {
		gauge(c.state, b2f(s.State == st), st)
	}

	// Temperatures are only meaningful once a reading has landed; before that
	// the monitor's zero values would publish as a real 0 °C.
	tip, ambient := math.NaN(), math.NaN()
	tipF, ambientF := math.NaN(), math.NaN()
	ambientAvg := math.NaN()
	if s.HasReading {
		tip, tipF = s.TipCelsius, s.TipFahrenheit
		ambient, ambientF = s.AmbientCelsius, s.AmbientFahrenheit
		ambientAvg = m.AmbientAvgCelsius
	}
	gauge(c.tipC, tip)
	gauge(c.tipF, tipF)
	gauge(c.ambientC, ambient)
	gauge(c.ambientF, ambientF)
	gauge(c.ambientAvgC, ambientAvg)

	// The target is user configuration and is always known.
	gauge(c.targetC, s.TargetCelsius)
	gauge(c.targetF, s.TargetFahrenheit)

	gauge(c.rate, s.RateCelsiusPerMin)
	gauge(c.etaSeconds, unknownAsNaN(s.ETASeconds))
	gauge(c.etaLow, unknownAsNaN(s.ETALowSeconds))
	gauge(c.etaHigh, unknownAsNaN(s.ETAHighSeconds))
	gauge(c.etaSamples, float64(s.ETASamples))

	src := s.ETASource
	if src == "" {
		src = "none"
	}
	for _, name := range allETASources {
		gauge(c.etaSource, b2f(name == src), name)
	}

	readyAt := math.NaN()
	if s.ETASeconds >= 0 {
		readyAt = float64(now.Add(time.Duration(s.ETASeconds)*time.Second).UnixNano()) / 1e9
	}
	gauge(c.readyAt, readyAt)

	prog := math.NaN()
	if p, ok := m.Progress(); ok {
		prog = p
	}
	gauge(c.progress, prog)

	remaining := math.NaN()
	if s.HasReading {
		remaining = math.Max(0, s.TargetCelsius-s.TipCelsius)
	}
	gauge(c.remainingC, remaining)

	gauge(c.cookInfo, 1, strconv.FormatInt(s.CookID, 10), s.CookName, s.MeatType)
	gauge(c.cookID, float64(s.CookID))
	gauge(c.cookStart, timestamp(m.CookStartedAt))
	gauge(c.cookDuration, since(now, m.CookStartedAt))
	gauge(c.cookSamples, float64(m.Samples))

	startTip, maxTip, maxAmb := math.NaN(), math.NaN(), math.NaN()
	if m.Samples > 0 {
		startTip, maxTip, maxAmb = s.StartTipCelsius, m.MaxTipCelsius, m.MaxAmbientCelsius
	}
	gauge(c.cookStartTip, startTip)
	gauge(c.cookMaxTip, maxTip)
	gauge(c.cookMaxAmb, maxAmb)

	gauge(c.updatedAt, timestamp(s.UpdatedAt))
	gauge(c.lastSample, timestamp(m.LastSampleAt))
	gauge(c.sampleAge, since(now, m.LastSampleAt))
	gauge(c.histCooks, float64(m.HistoryCooks))

	if c.st != nil {
		st, ok := c.dbStats()
		gauge(c.dbUp, b2f(ok))
		if ok {
			gauge(c.dbCooks, float64(st.Cooks))
			gauge(c.dbFinished, float64(st.FinishedCooks))
			gauge(c.dbSamples, float64(st.Samples))
		}
	}
}

// dbStats returns the cached database counts, refreshing them when stale. On a
// refresh error it keeps serving the last good values (with ok=false so
// meater_db_up reports the failure) rather than dropping the series.
func (c *Collector) dbStats() (store.Stats, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.storeStatOK && time.Since(c.storeStatAt) < storeStatsTTL {
		return c.storeStats, true
	}
	st, err := c.st.Stats()
	if err != nil {
		log.Printf("metrics: store stats: %v", err)
		return c.storeStats, false
	}
	c.storeStats = st
	c.storeStatOK = true
	c.storeStatAt = time.Now()
	return st, true
}

// Handler returns an HTTP handler serving the MEATER metrics together with the
// standard Go runtime and process collectors, on a private registry so nothing
// a dependency registers globally leaks in.
func Handler(mon *monitor.Monitor, st *store.Store) (http.Handler, error) {
	reg := prometheus.NewRegistry()
	if err := reg.Register(collectors.NewGoCollector()); err != nil {
		return nil, err
	}
	if err := reg.Register(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{})); err != nil {
		return nil, err
	}
	if err := reg.Register(NewCollector(mon, st)); err != nil {
		return nil, err
	}
	return promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}), nil
}

// unknownAsNaN maps the monitor's -1 "unknown" sentinel to NaN so it is not
// mistaken for a real duration.
func unknownAsNaN(v float64) float64 {
	if v < 0 {
		return math.NaN()
	}
	return v
}

// timestamp renders a time as fractional Unix seconds, or NaN when unset.
func timestamp(t time.Time) float64 {
	if t.IsZero() {
		return math.NaN()
	}
	return float64(t.UnixNano()) / 1e9
}

// since renders the age of a time in seconds, or NaN when unset.
func since(now, t time.Time) float64 {
	if t.IsZero() {
		return math.NaN()
	}
	return now.Sub(t).Seconds()
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}
