package monitor

import "time"

// Metrics is a snapshot of everything an external telemetry consumer (the
// Prometheus collector) needs. It exists so the collector never reaches into
// Monitor's internals, and so a scrape takes the lock exactly once instead of
// once per metric — a scrape mid-cook must not interleave with a reading and
// report a tip temperature from one sample against a rate from the next.
type Metrics struct {
	Status Status

	// Samples is the number of readings retained for the current cook.
	Samples int
	// CookStartedAt is the timestamp of the current cook's first reading; zero
	// when no cook is in progress.
	CookStartedAt time.Time
	// LastSampleAt is the timestamp of the most recent reading; zero when none.
	LastSampleAt time.Time
	// MaxTipCelsius and MaxAmbientCelsius are the current cook's running maxima.
	MaxTipCelsius     float64
	MaxAmbientCelsius float64
	// AmbientAvgCelsius is the smoothed chamber temperature the ETA model uses,
	// which is steadier than the instantaneous ambient reading.
	AmbientAvgCelsius float64
	// HistoryCooks is how many past cooks the learned time-to-target model
	// currently draws on.
	HistoryCooks int
}

// Metrics returns a consistent snapshot of the monitor's state for telemetry.
func (m *Monitor) Metrics() Metrics {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := Metrics{
		Status:       m.statusLocked(),
		Samples:      len(m.history),
		LastSampleAt: m.lastSampleAt,
		HistoryCooks: len(m.histModel),
	}
	if len(m.history) > 0 {
		out.CookStartedAt = m.history[0].at
		out.MaxTipCelsius = m.history[0].reading.TipCelsius
		out.MaxAmbientCelsius = m.history[0].reading.AmbientCelsius
		for _, s := range m.history {
			if s.reading.TipCelsius > out.MaxTipCelsius {
				out.MaxTipCelsius = s.reading.TipCelsius
			}
			if s.reading.AmbientCelsius > out.MaxAmbientCelsius {
				out.MaxAmbientCelsius = s.reading.AmbientCelsius
			}
		}
		out.AmbientAvgCelsius = m.ambientAvgLocked(ambientWindow)
	}
	return out
}

// Progress reports how far the current cook has climbed from its starting tip
// temperature toward the target, as a 0..1 fraction. ok is false when there is
// nothing meaningful to measure; see progressLocked.
func (m Metrics) Progress() (float64, bool) {
	if m.Status.ProgressPercent < 0 {
		return 0, false
	}
	return m.Status.ProgressPercent / 100, true
}
