package monitor

import (
	"testing"
	"time"

	"github.com/awlx/meater-golang/internal/store"
)

// synthCook builds a finished cook whose tip rises through three phases — a
// brisk initial climb, a long stall (plateau), and a final climb to the
// target — at one sample per minute, with a steady chamber temperature.
func synthCook(meat string, target, chamber float64) store.CookMeta {
	return store.CookMeta{Name: meat, MeatType: meat, TargetCelsius: target, MaxAmbientCelsius: chamber}
}

func synthSamples(chamber float64) []store.Point {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	var pts []store.Point
	tip := 20.0
	add := func(mins int, perMin float64) {
		for i := 0; i < mins; i++ {
			pts = append(pts, store.Point{
				At:             start,
				TipCelsius:     tip,
				AmbientCelsius: chamber,
			})
			start = start.Add(time.Minute)
			tip += perMin
		}
	}
	add(60, 0.8)  // 20 -> 68 over 60 min
	add(90, 0.05) // 68 -> ~72.5 over 90 min (the stall)
	add(60, 0.4)  // ~72.5 -> ~96.5 over 60 min
	return pts
}

func TestBuildHistCookReachMonotonic(t *testing.T) {
	hc, ok := buildHistCook(synthCook("pork neck", 95, 110), synthSamples(110))
	if !ok {
		t.Fatal("expected cook to build")
	}
	for i := 1; i < len(hc.reach); i++ {
		if hc.reach[i].temp < hc.reach[i-1].temp {
			t.Fatalf("reach temps not ascending at %d", i)
		}
		if hc.reach[i].sec < hc.reach[i-1].sec {
			t.Fatalf("reach seconds not ascending at %d", i)
		}
	}
	// Reaching the target should take the full ~210 minutes of the cook.
	tT := hc.reachTime(95)
	if tT < 150*60 || tT > 230*60 {
		t.Fatalf("reachTime(95)=%.0fs, want ~210min", tT)
	}
}

func TestHistoricalETAUsesPastCookThroughStall(t *testing.T) {
	hc, ok := buildHistCook(synthCook("pork neck", 95, 110), synthSamples(110))
	if !ok {
		t.Fatal("build failed")
	}
	m := &Monitor{histModel: []histCook{hc}}

	// Entering the stall at tip 68: physics reads a near-zero rate and gives
	// up, but the historical estimate knows the long plateau plus final climb
	// remains — well over an hour.
	eta, n, lo, hi := m.historicalETALocked(68, 110, 95, "pork neck")
	if n != 1 {
		t.Fatalf("matches=%d, want 1", n)
	}
	if eta < 90*60 {
		t.Fatalf("historical eta=%.0fs, want > 90min through the stall", eta)
	}
	if lo > eta || hi < eta {
		t.Fatalf("range [%.0f,%.0f] does not bracket eta %.0f", lo, hi, eta)
	}
}

func TestHistoricalETAMeatTypeFallback(t *testing.T) {
	hc, _ := buildHistCook(synthCook("brisket", 95, 110), synthSamples(110))
	m := &Monitor{histModel: []histCook{hc}}

	// No exact match for "pork neck": fall back to all comparable cooks rather
	// than returning unknown.
	eta, n, _, _ := m.historicalETALocked(50, 110, 95, "pork neck")
	if n != 1 || eta <= 0 {
		t.Fatalf("expected fallback match, got n=%d eta=%.0f", n, eta)
	}
}

func TestHistoricalETANoComparableCook(t *testing.T) {
	// A past cook that only ever reached 70 cannot inform a 95 target — it
	// fell well outside the pulled-short tolerance.
	hc, _ := buildHistCook(synthCook("pork neck", 70, 110), synthSamples(110))
	hc.maxReach = 72 // pretend it stopped in the stall
	m := &Monitor{histModel: []histCook{hc}}
	if eta, n, _, _ := m.historicalETALocked(50, 110, 95, "pork neck"); n != 0 || eta != -1 {
		t.Fatalf("expected no match, got n=%d eta=%.0f", n, eta)
	}
}

// synthSamplesTo is synthSamples but with the final climb ending near maxTip,
// modelling a cook pulled probe-tender a few degrees below the round target.
func synthSamplesTo(chamber, maxTip float64) []store.Point {
	start := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	var pts []store.Point
	tip := 20.0
	add := func(mins int, perMin float64) {
		for i := 0; i < mins; i++ {
			pts = append(pts, store.Point{At: start, TipCelsius: tip, AmbientCelsius: chamber})
			start = start.Add(time.Minute)
			tip += perMin
		}
	}
	add(60, 0.8)  // 20 -> 68
	add(90, 0.05) // stall to ~72.5
	mins := int((maxTip - 72.5) / 0.4)
	add(mins, 0.4) // climb to ~maxTip then the cook is pulled
	return pts
}

func TestHistoricalETAExtrapolatesPulledShortCook(t *testing.T) {
	// A past cook pulled at ~91.7 °C (within tolerance of a 95 °C target) should
	// still inform the estimate, extrapolating the small final gap.
	hc, ok := buildHistCook(synthCook("pork neck", 95, 110), synthSamplesTo(110, 91.7))
	if !ok {
		t.Fatal("build failed")
	}
	if hc.maxReach >= 95 {
		t.Fatalf("maxReach=%.1f, want < target", hc.maxReach)
	}
	m := &Monitor{histModel: []histCook{hc}}
	eta, n, _, _ := m.historicalETALocked(80, 110, 95, "pork neck")
	if n != 1 || eta <= 0 {
		t.Fatalf("expected extrapolated match, got n=%d eta=%.0f", n, eta)
	}
}

func TestBlendETACarriesHistoryWhenPhysicsUnknown(t *testing.T) {
	eta, src, _, _ := blendETA(-1, 3600, 2, 3000, 4200)
	if src != "history" || eta != 3600 {
		t.Fatalf("got %s %.0f, want history 3600", src, eta)
	}
}

func TestBlendETACombinesWhenBothKnown(t *testing.T) {
	// Three matches => full historical weight (capped at historyBlendCap).
	eta, src, _, _ := blendETA(3600, 7200, 3, 7200, 7200)
	want := historyBlendCap*7200 + (1-historyBlendCap)*3600
	if src != "blend" {
		t.Fatalf("source=%s, want blend", src)
	}
	if eta < want-1 || eta > want+1 {
		t.Fatalf("blend eta=%.0f, want %.0f", eta, want)
	}
}

func TestBlendETAPhysicsOnly(t *testing.T) {
	eta, src, lo, hi := blendETA(1800, -1, 0, -1, -1)
	if src != "physics" || eta != 1800 || lo != -1 || hi != -1 {
		t.Fatalf("got %s %.0f [%.0f,%.0f]", src, eta, lo, hi)
	}
}
