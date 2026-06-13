package monitor

import (
	"log"
	"sort"
	"strings"

	"github.com/awlx/meater-golang/internal/store"
)

// histCook is a compact summary of one finished cook, learned from its stored
// samples and used to estimate the time remaining on the current cook by
// analogy. Rather than keep every sample we keep the cook's "rise curve": the
// elapsed time at which the tip first reached each temperature. Because the
// curve is built from the running maximum tip, a stall (the long evaporative
// plateau) simply shows up as time advancing while the temperature barely
// moves — exactly the part a physics-only model gets wrong.
type histCook struct {
	meatType   string
	target     float64
	chamberAvg float64   // mean ambient (cook chamber) over the cook
	maxReach   float64   // highest tip temperature the cook reached
	reach      []reachPt // tip temperature -> elapsed seconds, temp ascending
}

// reachPt records that the cook first reached temp at elapsed seconds.
type reachPt struct {
	temp float64
	sec  float64
}

// LoadHistory rebuilds the in-memory model from the most recent finished cooks.
// It performs all database I/O without holding the monitor lock, then swaps the
// finished model in under the lock, so it is safe to call from startup or after
// a cook ends without blocking status reads.
func (m *Monitor) LoadHistory() {
	m.mu.RLock()
	st := m.st
	m.mu.RUnlock()
	if st == nil {
		return
	}

	cooks, err := st.FinishedCooks(historyModelCooks)
	if err != nil {
		log.Printf("history: list finished cooks: %v", err)
		return
	}

	model := make([]histCook, 0, len(cooks))
	for _, c := range cooks {
		pts, err := st.CookSamples(c.ID)
		if err != nil {
			log.Printf("history: cook #%d samples: %v", c.ID, err)
			continue
		}
		if hc, ok := buildHistCook(c, pts); ok {
			model = append(model, hc)
		}
	}

	m.mu.Lock()
	m.histModel = model
	m.mu.Unlock()
	if len(model) > 0 {
		log.Printf("history: learned time-to-target model from %d past cook(s)", len(model))
	}
}

// buildHistCook condenses a finished cook's samples into a rise curve. It
// returns ok=false when the cook is too short or never rose enough to be useful
// for estimation.
func buildHistCook(c store.CookMeta, pts []store.Point) (histCook, bool) {
	if len(pts) < 3 {
		return histCook{}, false
	}
	start := pts[0].At
	var ambientSum float64
	runMax := pts[0].TipCelsius
	reach := []reachPt{{temp: runMax, sec: 0}}
	for _, p := range pts {
		ambientSum += p.AmbientCelsius
		if p.TipCelsius > runMax {
			runMax = p.TipCelsius
			reach = append(reach, reachPt{
				temp: runMax,
				sec:  p.At.Sub(start).Seconds(),
			})
		}
	}
	// Need a meaningful rise to learn anything from.
	if runMax-pts[0].TipCelsius < 5 {
		return histCook{}, false
	}
	return histCook{
		meatType:   normalizeMeat(c.MeatType),
		target:     c.TargetCelsius,
		chamberAvg: ambientSum / float64(len(pts)),
		maxReach:   runMax,
		reach:      reach,
	}, true
}

// reachTime returns the elapsed seconds at which the cook first reached temp,
// linearly interpolating between recorded points. It returns -1 when the cook
// never reached temp. Below the first recorded temperature it returns 0 (the
// cook was already that warm when recording began).
func (h histCook) reachTime(temp float64) float64 {
	if len(h.reach) == 0 {
		return -1
	}
	if temp <= h.reach[0].temp {
		return 0
	}
	if temp > h.maxReach {
		return -1
	}
	for i := 1; i < len(h.reach); i++ {
		if h.reach[i].temp >= temp {
			lo := h.reach[i-1]
			hi := h.reach[i]
			span := hi.temp - lo.temp
			if span <= 0 {
				return hi.sec
			}
			frac := (temp - lo.temp) / span
			return lo.sec + frac*(hi.sec-lo.sec)
		}
	}
	return h.reach[len(h.reach)-1].sec
}

// historicalETALocked estimates the seconds remaining until the tip reaches
// target by analogy with past cooks: for each comparable cook it measures how
// long that cook took to climb from the current tip temperature to the target,
// scales it for how much hotter or cooler today's chamber is, and takes the
// median. It returns -1 with n=0 when no past cook is comparable. The caller
// must hold at least a read lock.
//
// Matching prefers cooks of the same meat type when any exist; otherwise it
// falls back to all comparable cooks, so a new cut still benefits from the
// smoker's general behaviour. low/high bound the spread of the matched cooks
// for an honest range in the UI.
func (m *Monitor) historicalETALocked(tip, chamber, target float64, meatType string) (eta float64, n int, low, high float64) {
	want := normalizeMeat(meatType)

	var all, exact []float64
	for _, hc := range m.histModel {
		// The past cook must have climbed through both the current tip and the
		// target for the interval to be measurable.
		if hc.maxReach < target || hc.maxReach < tip {
			continue
		}
		tStart := hc.reachTime(tip)
		tEnd := hc.reachTime(target)
		if tStart < 0 || tEnd < 0 {
			continue
		}
		rem := tEnd - tStart
		if rem <= 0 {
			continue
		}
		// Scale for chamber temperature: a hotter chamber today than in the
		// past cook means a larger driving gap and so less time, and vice
		// versa. Clamp the adjustment so one odd cook can't dominate.
		gapNow := chamber - tip
		gapHist := hc.chamberAvg - tip
		if gapNow > 0.5 && gapHist > 0.5 {
			rem *= clampF(gapHist/gapNow, 0.5, 2)
		}
		all = append(all, rem)
		if want != "" && hc.meatType == want {
			exact = append(exact, rem)
		}
	}

	use := all
	if want != "" && len(exact) > 0 {
		use = exact
	}
	if len(use) == 0 {
		return -1, 0, -1, -1
	}

	sort.Float64s(use)
	return medianSorted(use), len(use), use[0], use[len(use)-1]
}

// blendETA combines the physics estimate with the learned historical estimate.
// Confidence in history grows with the number of matching cooks (full at three)
// but is capped so physics always tempers it. When physics is unknown (a deep
// stall) the history estimate is used on its own, which is the case it helps
// most. It returns the blended seconds (-1 when nothing is known), a source
// label, and a low/high range (-1 when unavailable).
func blendETA(phys, hist float64, nHist int, lo, hi float64) (eta float64, source string, low, high float64) {
	if hist < 0 {
		if phys < 0 {
			return -1, "", -1, -1
		}
		return phys, "physics", -1, -1
	}
	w := clampF(float64(nHist)/3, 0, 1) * historyBlendCap
	if phys < 0 {
		return hist, "history", lo, hi
	}
	blend := w*hist + (1-w)*phys
	return blend, "blend", w*lo + (1-w)*phys, w*hi + (1-w)*phys
}

// normalizeMeat lower-cases and trims a meat-type label so "Pork Neck" and
// "pork neck" match.
func normalizeMeat(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

func clampF(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// medianSorted returns the median of an already-sorted slice.
func medianSorted(s []float64) float64 {
	n := len(s)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
