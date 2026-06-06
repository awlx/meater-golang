// Package monitor maintains the live state of a MEATER probe: the latest
// reading, a short history used to estimate the cooking rate and time
// remaining, the user's target temperature, and a publish/subscribe mechanism
// so HTTP clients can stream updates.
package monitor

import (
	"sync"
	"time"

	"github.com/awlx/meater-golang/internal/meater"
)

// rateWindow is how far back the cooking-rate regression looks.
const rateWindow = 3 * time.Minute

// historyLimit caps the number of retained samples.
const historyLimit = 4096

// sample is a single timestamped reading.
type sample struct {
	at      time.Time
	reading meater.Reading
}

// Status is an immutable snapshot of the probe state, serialised to JSON for
// the API and the web UI.
type Status struct {
	Connected         bool      `json:"connected"`
	TipCelsius        float64   `json:"tipCelsius"`
	TipFahrenheit     float64   `json:"tipFahrenheit"`
	AmbientCelsius    float64   `json:"ambientCelsius"`
	AmbientFahrenheit float64   `json:"ambientFahrenheit"`
	TargetCelsius     float64   `json:"targetCelsius"`
	TargetFahrenheit  float64   `json:"targetFahrenheit"`
	RateCelsiusPerMin float64   `json:"rateCelsiusPerMin"`
	ETASeconds        float64   `json:"etaSeconds"` // -1 when unknown
	State             string    `json:"state"`
	HasReading        bool      `json:"hasReading"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// Cooking states reported to clients.
const (
	StateDisconnected = "disconnected"
	StateWaiting      = "waiting"
	StateCooking      = "cooking"
	StateStalled      = "stalled"
	StateReady        = "ready"
)

// Monitor is a concurrency-safe holder of probe state.
type Monitor struct {
	mu        sync.RWMutex
	history   []sample
	latest    meater.Reading
	hasRead   bool
	connected bool
	target    float64 // Celsius
	updatedAt time.Time
	subs      map[chan Status]struct{}
}

// New returns a Monitor with the given default target temperature in Celsius.
func New(targetCelsius float64) *Monitor {
	return &Monitor{
		target: targetCelsius,
		subs:   make(map[chan Status]struct{}),
	}
}

// SetConnected records the BLE connection state and notifies subscribers.
func (m *Monitor) SetConnected(connected bool) {
	m.mu.Lock()
	m.connected = connected
	if !connected {
		m.hasRead = false
	}
	status := m.statusLocked()
	m.mu.Unlock()
	m.broadcast(status)
}

// Update records a new reading and notifies subscribers.
func (m *Monitor) Update(r meater.Reading) {
	now := time.Now()
	m.mu.Lock()
	m.latest = r
	m.hasRead = true
	m.connected = true
	m.updatedAt = now
	m.history = append(m.history, sample{at: now, reading: r})
	if len(m.history) > historyLimit {
		m.history = m.history[len(m.history)-historyLimit:]
	}
	status := m.statusLocked()
	m.mu.Unlock()
	m.broadcast(status)
}

// SetTarget changes the target tip temperature (Celsius) and notifies
// subscribers so the ETA refreshes immediately.
func (m *Monitor) SetTarget(celsius float64) {
	m.mu.Lock()
	m.target = celsius
	status := m.statusLocked()
	m.mu.Unlock()
	m.broadcast(status)
}

// Status returns the current snapshot.
func (m *Monitor) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.statusLocked()
}

// Point is a single historical reading for charting.
type Point struct {
	At             time.Time `json:"at"`
	TipCelsius     float64   `json:"tipCelsius"`
	AmbientCelsius float64   `json:"ambientCelsius"`
}

// History returns the retained samples (oldest first) for plotting.
func (m *Monitor) History() []Point {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Point, len(m.history))
	for i, s := range m.history {
		out[i] = Point{
			At:             s.at,
			TipCelsius:     round1(s.reading.TipCelsius),
			AmbientCelsius: round1(s.reading.AmbientCelsius),
		}
	}
	return out
}

// Subscribe registers a channel that receives every future status update.
// The returned cancel function unregisters and closes the channel.
func (m *Monitor) Subscribe() (<-chan Status, func()) {
	ch := make(chan Status, 8)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()

	cancel := func() {
		m.mu.Lock()
		if _, ok := m.subs[ch]; ok {
			delete(m.subs, ch)
			close(ch)
		}
		m.mu.Unlock()
	}
	return ch, cancel
}

// broadcast sends a status to all subscribers without blocking; slow consumers
// simply miss intermediate updates.
func (m *Monitor) broadcast(s Status) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for ch := range m.subs {
		select {
		case ch <- s:
		default:
		}
	}
}

// statusLocked builds a snapshot. The caller must hold at least a read lock.
func (m *Monitor) statusLocked() Status {
	s := Status{
		Connected:        m.connected,
		HasReading:       m.hasRead,
		TargetCelsius:    round1(m.target),
		TargetFahrenheit: round1(celsiusToFahrenheit(m.target)),
		ETASeconds:       -1,
		UpdatedAt:        m.updatedAt,
		State:            StateDisconnected,
	}

	if !m.connected {
		return s
	}
	if !m.hasRead {
		s.State = StateWaiting
		return s
	}

	tip := m.latest.TipCelsius
	s.TipCelsius = round1(tip)
	s.TipFahrenheit = round1(m.latest.TipFahrenheit())
	s.AmbientCelsius = round1(m.latest.AmbientCelsius)
	s.AmbientFahrenheit = round1(m.latest.AmbientFahrenheit())

	ratePerSec, ok := m.rateLocked()
	s.RateCelsiusPerMin = round1(ratePerSec * 60)

	switch {
	case tip >= m.target:
		s.State = StateReady
		s.ETASeconds = 0
	case !ok || ratePerSec <= 1e-4:
		s.State = StateStalled
	default:
		s.State = StateCooking
		s.ETASeconds = (m.target - tip) / ratePerSec
	}
	return s
}

// rateLocked computes the tip temperature rise in Celsius per second using a
// least-squares fit over the recent history window. The caller must hold the
// lock. ok is false when there is not enough data.
func (m *Monitor) rateLocked() (perSec float64, ok bool) {
	if len(m.history) < 2 {
		return 0, false
	}
	now := time.Now()
	cutoff := now.Add(-rateWindow)

	var n, sumX, sumY, sumXY, sumXX float64
	for _, smp := range m.history {
		if smp.at.Before(cutoff) {
			continue
		}
		x := smp.at.Sub(now).Seconds() // seconds in the past (negative)
		y := smp.reading.TipCelsius
		n++
		sumX += x
		sumY += y
		sumXY += x * y
		sumXX += x * x
	}
	if n < 2 {
		return 0, false
	}
	denom := n*sumXX - sumX*sumX
	if denom == 0 {
		return 0, false
	}
	slope := (n*sumXY - sumX*sumY) / denom
	return slope, true
}

func round1(v float64) float64 {
	return float64(int(v*10+sign(v)*0.5)) / 10
}

func sign(v float64) float64 {
	if v < 0 {
		return -1
	}
	return 1
}

func celsiusToFahrenheit(c float64) float64 {
	return c*9.0/5.0 + 32.0
}
