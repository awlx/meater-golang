// Package monitor maintains the live state of a MEATER probe: the latest
// reading, a short history used to estimate the cooking rate and time
// remaining, the user's target temperature, and a publish/subscribe mechanism
// so HTTP clients can stream updates.
package monitor

import (
	"log"
	"math"
	"sync"
	"time"

	"github.com/awlx/meater-golang/internal/meater"
	"github.com/awlx/meater-golang/internal/store"
)

// rateWindow is how far back the cooking-rate regression looks for the live
// displayed rate. It is short so the figure stays responsive on fast cooks
// (a steak is done in minutes).
const rateWindow = 3 * time.Minute

// etaRateWindow smooths the cooking rate over a longer span than rateWindow
// specifically for the time-to-target estimate. The stall — the evaporative
// plateau where a large cut (pork shoulder, brisket) barely rises for an hour
// or more — makes a short window read a near-zero rate and throw the ETA to
// infinity. Averaging over a longer span keeps the estimate stable through it.
// The window is only a maximum lookback, so early/short cooks simply use
// whatever history they have.
const etaRateWindow = 12 * time.Minute

// stallRatePerSec is the rate below which the cook is treated as stalled rather
// than progressing. 0.01 °C/min (0.6 °C/hour) is well into plateau territory.
const stallRatePerSec = 0.01 / 60

// etaMaxSeconds caps the estimate. Beyond this the rate is so low (a deep
// stall) that any number is meaningless, so the UI reports "unknown" instead
// of an absurd multi-day figure.
const etaMaxSeconds = 24 * 60 * 60

// ambientWindow is how far back to average the ambient (cook chamber)
// temperature when using it as the asymptote for the ETA model.
const ambientWindow = 5 * time.Minute

// historyLimit caps the number of retained samples. It is large enough to hold
// the full span of even a very long cook (a multi-day smoke at one sample every
// few seconds) so the live chart never drops the early part of the curve.
const historyLimit = 200000

// defaultIdleTimeout is how long without a reading marks the current cook as
// finished. It is generous so a transient BLE drop/reconnect does not split a
// long cook into two sessions.
const defaultIdleTimeout = 30 * time.Minute

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
	Running           bool      `json:"running"` // probe discovery is active
	CookName          string    `json:"cookName"`
	CookID            int64     `json:"cookId"`
	UpdatedAt         time.Time `json:"updatedAt"`
}

// Cooking states reported to clients.
const (
	StateIdle         = "idle" // discovery stopped; waiting for Start
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

	// Persistence and cook bookkeeping (st may be nil to disable).
	st           *store.Store
	cookID       int64
	cookName     string
	pendingName  string // applied to the next cook that auto-starts
	lastSampleAt time.Time
	idleTimeout  time.Duration

	// Discovery control. running gates the BLE loop so the app only scans
	// for the probe after the user presses Start. startCond wakes the loop
	// when running flips true; stopCh is closed on Stop to interrupt an
	// in-progress scan or stream.
	running   bool
	startCond *sync.Cond
	stopCh    chan struct{}
}

// New returns a Monitor with the given default target temperature in Celsius.
func New(targetCelsius float64) *Monitor {
	m := &Monitor{
		target:      targetCelsius,
		subs:        make(map[chan Status]struct{}),
		idleTimeout: defaultIdleTimeout,
	}
	m.startCond = sync.NewCond(&m.mu)
	// Start stopped: a closed channel reflects "not running" until Start.
	m.stopCh = make(chan struct{})
	close(m.stopCh)
	return m
}

// Start begins probe discovery and marks a fresh cook: the next reading after
// Start opens a new cook session. It is a no-op if already running.
func (m *Monitor) Start() {
	now := time.Now()
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.stopCh = make(chan struct{})
	oldID := m.cookID
	// Begin a fresh cook; the first reading after Start opens it.
	m.cookID = 0
	m.history = m.history[:0]
	m.hasRead = false
	m.lastSampleAt = time.Time{}
	st := m.st
	m.startCond.Broadcast()
	status := m.statusLocked()
	m.mu.Unlock()
	if st != nil && oldID != 0 {
		if err := st.EndCook(oldID, now); err != nil {
			log.Printf("store: end cook: %v", err)
		}
		if err := st.Prune(); err != nil {
			log.Printf("store: prune: %v", err)
		}
	}
	m.broadcast(status)
}

// Stop halts probe discovery, disconnects, and ends the current cook. It is a
// no-op if already stopped.
func (m *Monitor) Stop() {
	now := time.Now()
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return
	}
	m.running = false
	close(m.stopCh)
	m.connected = false
	m.hasRead = false
	oldID := m.cookID
	m.cookID = 0
	st := m.st
	status := m.statusLocked()
	m.mu.Unlock()
	if st != nil && oldID != 0 {
		if err := st.EndCook(oldID, now); err != nil {
			log.Printf("store: end cook: %v", err)
		}
		if err := st.Prune(); err != nil {
			log.Printf("store: prune: %v", err)
		}
	}
	m.broadcast(status)
}

// Running reports whether probe discovery is currently active.
func (m *Monitor) Running() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

// WaitForStart blocks until discovery is started. The BLE loop calls it before
// scanning so nothing happens in the background until the user presses Start.
func (m *Monitor) WaitForStart() {
	m.mu.Lock()
	for !m.running {
		m.startCond.Wait()
	}
	m.mu.Unlock()
}

// StopChan returns a channel that is closed when discovery is stopped. The BLE
// loop selects on it to abort an in-progress scan or stream.
func (m *Monitor) StopChan() <-chan struct{} {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.stopCh
}

// SetStore attaches a persistence backend. idleTimeout overrides the default
// when > 0.
func (m *Monitor) SetStore(st *store.Store, idleTimeout time.Duration) {
	m.mu.Lock()
	m.st = st
	if idleTimeout > 0 {
		m.idleTimeout = idleTimeout
	}
	m.mu.Unlock()
}

// Resume restores an in-progress cook (its id, name, target, and samples) so a
// restart keeps the live chart and keeps appending to the same session.
func (m *Monitor) Resume(cookID int64, name string, target float64, pts []Point) {
	m.mu.Lock()
	m.cookID = cookID
	m.cookName = name
	m.pendingName = name
	if target > 0 {
		m.target = target
	}
	m.history = m.history[:0]
	for _, p := range pts {
		m.history = append(m.history, sample{
			at:      p.At,
			reading: meater.Reading{TipCelsius: p.TipCelsius, AmbientCelsius: p.AmbientCelsius},
		})
	}
	if len(m.history) > historyLimit {
		m.history = m.history[len(m.history)-historyLimit:]
	}
	if n := len(m.history); n > 0 {
		m.lastSampleAt = m.history[n-1].at
	}
	// Resuming a live cook means discovery should be active immediately.
	m.running = true
	m.stopCh = make(chan struct{})
	m.startCond.Broadcast()
	m.mu.Unlock()
}

// EnableDiscovery starts probe discovery without resuming a cook, so the BLE
// loop scans and the next reading opens a fresh cook. It is used on startup
// when a previous cook was too old to resume but the app should still reconnect
// to the probe on its own rather than sitting idle waiting for Start.
func (m *Monitor) EnableDiscovery() {
	m.mu.Lock()
	if !m.running {
		m.running = true
		m.stopCh = make(chan struct{})
		m.startCond.Broadcast()
	}
	status := m.statusLocked()
	m.mu.Unlock()
	m.broadcast(status)
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

// Update records a new reading and notifies subscribers. It also persists the
// sample to the current cook, auto-starting a new cook on the first reading
// after an idle period. Update is expected to be called by a single producer
// (the BLE notification callback or the mock loop), so the cook-start check is
// race-free.
func (m *Monitor) Update(r meater.Reading) {
	now := time.Now()
	m.mu.Lock()
	m.latest = r
	m.hasRead = true
	m.connected = true
	m.updatedAt = now
	needStart := m.st != nil && m.cookID == 0
	if needStart {
		// Fresh cook: start its history from this reading.
		m.history = m.history[:0]
	}
	m.history = append(m.history, sample{at: now, reading: r})
	if len(m.history) > historyLimit {
		m.history = m.history[len(m.history)-historyLimit:]
	}
	m.lastSampleAt = now
	st := m.st
	cookID := m.cookID
	name := m.pendingName
	target := m.target
	m.mu.Unlock()

	if st != nil {
		if needStart {
			if id, err := st.StartCook(name, target, now); err != nil {
				log.Printf("store: start cook: %v", err)
			} else {
				m.mu.Lock()
				m.cookID = id
				if name != "" {
					m.cookName = name
				}
				m.mu.Unlock()
				cookID = id
			}
		}
		if cookID != 0 {
			if err := st.AppendSample(cookID, now, r.TipCelsius, r.AmbientCelsius); err != nil {
				log.Printf("store: append sample: %v", err)
			}
		}
	}

	m.mu.RLock()
	status := m.statusLocked()
	m.mu.RUnlock()
	m.broadcast(status)
}

// SetTarget changes the target tip temperature (Celsius) and notifies
// subscribers so the ETA refreshes immediately.
func (m *Monitor) SetTarget(celsius float64) {
	m.mu.Lock()
	m.target = celsius
	st := m.st
	cookID := m.cookID
	status := m.statusLocked()
	m.mu.Unlock()
	if st != nil && cookID != 0 {
		if err := st.SetCookTarget(cookID, celsius); err != nil {
			log.Printf("store: set target: %v", err)
		}
	}
	m.broadcast(status)
}

// SetCookName names the current (or next) cook.
func (m *Monitor) SetCookName(name string) {
	m.mu.Lock()
	m.cookName = name
	m.pendingName = name
	st := m.st
	cookID := m.cookID
	status := m.statusLocked()
	m.mu.Unlock()
	if st != nil && cookID != 0 {
		if err := st.RenameCook(cookID, name); err != nil {
			log.Printf("store: rename cook: %v", err)
		}
	}
	m.broadcast(status)
}

// NewCook ends the current cook (if any) and clears the live chart so the next
// reading begins a fresh session. The optional name is applied to the new cook.
func (m *Monitor) NewCook(name string) {
	now := time.Now()
	m.mu.Lock()
	oldID := m.cookID
	m.cookID = 0
	m.cookName = name
	m.pendingName = name
	m.history = m.history[:0]
	m.hasRead = false
	m.lastSampleAt = time.Time{}
	st := m.st
	status := m.statusLocked()
	m.mu.Unlock()
	if st != nil && oldID != 0 {
		if err := st.EndCook(oldID, now); err != nil {
			log.Printf("store: end cook: %v", err)
		}
		if err := st.Prune(); err != nil {
			log.Printf("store: prune: %v", err)
		}
	}
	m.broadcast(status)
}

// RunJanitor periodically finishes the current cook once the probe has been
// idle longer than idleTimeout. It blocks; run it in a goroutine.
func (m *Monitor) RunJanitor() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for range t.C {
		m.mu.Lock()
		id := m.cookID
		last := m.lastSampleAt
		idle := m.idleTimeout
		st := m.st
		m.mu.Unlock()
		if st == nil || id == 0 || last.IsZero() {
			continue
		}
		if time.Since(last) > idle {
			if err := st.EndCook(id, last); err != nil {
				log.Printf("store: end idle cook: %v", err)
			}
			if err := st.Prune(); err != nil {
				log.Printf("store: prune: %v", err)
			}
			m.mu.Lock()
			if m.cookID == id {
				m.cookID = 0
			}
			m.mu.Unlock()
		}
	}
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
		Running:          m.running,
		TargetCelsius:    round1(m.target),
		TargetFahrenheit: round1(celsiusToFahrenheit(m.target)),
		ETASeconds:       -1,
		CookName:         m.cookName,
		CookID:           m.cookID,
		UpdatedAt:        m.updatedAt,
		State:            StateDisconnected,
	}

	if !m.running {
		s.State = StateIdle
		return s
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
	s.RateCelsiusPerMin = round2(ratePerSec * 60)

	// The ETA uses a rate smoothed over a longer window so the stall does not
	// throw it to infinity; the displayed rate above stays short and responsive.
	etaRate, etaOK := m.rateLockedWindow(etaRateWindow)

	switch {
	case tip >= m.target:
		s.State = StateReady
		s.ETASeconds = 0
	case !ok || ratePerSec <= 1e-4:
		s.State = StateStalled
	default:
		eta := -1.0
		if etaOK && etaRate > stallRatePerSec {
			eta = etaSeconds(tip, m.target, m.ambientAvgLocked(ambientWindow), etaRate)
		}
		if eta < 0 {
			// Rate too low or so far out the estimate is meaningless: report a
			// stall (with an unknown ETA) rather than a misleading number.
			s.State = StateStalled
		} else {
			s.State = StateCooking
			s.ETASeconds = eta
		}
	}
	return s
}

// etaSeconds estimates the seconds until the tip reaches target using Newton's
// law of cooling: the tip approaches the cook-chamber (ambient) temperature
// exponentially, so its rise decelerates as it nears ambient. Modelling that
// curve avoids the wildly optimistic estimate a straight-line extrapolation
// produces during the fast initial rise — the reason a pulled pork looked
// "done in 2h" when it is really many hours away.
//
// With dT/dt = k·(ambient − tip) the instantaneous rate gives k = rate/(ambient
// − tip), and the time to reach target is ln((ambient − tip)/(ambient −
// target)) / k. The estimate is always longer than the linear one (because
// −ln(1−x) > x), and it stretches automatically as the rate falls off in the
// stall. It falls back to a straight line only when the chamber is not yet
// meaningfully hotter than the target, so the UI still shows something. The
// result is capped at etaMaxSeconds: a deep stall produces an arbitrarily large
// number that is better reported as "unknown" (-1) than shown literally.
//
// Note this model is meat-agnostic by design — like the official app it reads
// the cut's thermal mass straight off the measured rise rate and the chamber
// temperature rather than from a per-meat-type table.
func etaSeconds(tip, target, ambient, ratePerSec float64) float64 {
	if ratePerSec <= 0 || target <= tip {
		return -1
	}
	gapNow := ambient - tip
	gapTarget := ambient - target
	var eta float64
	if gapTarget >= 1 && gapNow > gapTarget {
		k := ratePerSec / gapNow
		eta = math.Log(gapNow/gapTarget) / k
	} else {
		eta = (target - tip) / ratePerSec
	}
	if eta > etaMaxSeconds {
		return -1
	}
	return eta
}

// ambientAvgLocked averages the ambient (cook chamber) temperature over the
// recent window so a single noisy sample doesn't swing the ETA. It falls back
// to the latest ambient when the window holds no samples. The caller must hold
// at least a read lock.
func (m *Monitor) ambientAvgLocked(window time.Duration) float64 {
	if len(m.history) == 0 {
		return m.latest.AmbientCelsius
	}
	cutoff := time.Now().Add(-window)
	var sum float64
	var n int
	for i := len(m.history) - 1; i >= 0; i-- {
		if m.history[i].at.Before(cutoff) {
			break
		}
		sum += m.history[i].reading.AmbientCelsius
		n++
	}
	if n == 0 {
		return m.latest.AmbientCelsius
	}
	return sum / float64(n)
}

// rateLocked computes the tip temperature rise in Celsius per second over the
// short live-display window. The caller must hold the lock.
func (m *Monitor) rateLocked() (perSec float64, ok bool) {
	return m.rateLockedWindow(rateWindow)
}

// rateLockedWindow computes the tip temperature rise in Celsius per second
// using a least-squares fit over the most recent window. The caller must hold
// the lock. ok is false when there is not enough data. A longer window smooths
// the stall for the ETA; a shorter one keeps the live rate responsive.
func (m *Monitor) rateLockedWindow(window time.Duration) (perSec float64, ok bool) {
	if len(m.history) < 2 {
		return 0, false
	}
	now := time.Now()
	cutoff := now.Add(-window)

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

func round2(v float64) float64 {
	return float64(int(v*100+sign(v)*0.5)) / 100
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
