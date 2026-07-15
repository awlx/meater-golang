package metrics

import (
	"math"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/awlx/meater-golang/internal/meater"
	"github.com/awlx/meater-golang/internal/monitor"
)

// scrape renders the exporter output for a monitor.
func scrape(t *testing.T, mon *monitor.Monitor) string {
	t.Helper()
	h, err := Handler(mon, nil)
	if err != nil {
		t.Fatalf("Handler: %v", err)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	if rec.Code != 200 {
		t.Fatalf("scrape status = %d, want 200", rec.Code)
	}
	return rec.Body.String()
}

// hasLine reports whether the exposition contains the exact series line.
func hasLine(body, want string) bool {
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == want {
			return true
		}
	}
	return false
}

// value returns the value of a series by name (including any labels). It fails
// the test when the series is absent.
func value(t *testing.T, body, series string) float64 {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		name, v, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || name != series {
			continue
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			t.Fatalf("series %s has unparseable value %q: %v", series, v, err)
		}
		return f
	}
	t.Fatalf("scrape missing series %s", series)
	return 0
}

// closeTo asserts a series is within tol of want, so an assertion on a computed
// ratio does not hinge on float formatting.
func closeTo(t *testing.T, body, series string, want, tol float64) {
	t.Helper()
	if got := value(t, body, series); math.Abs(got-want) > tol {
		t.Errorf("%s = %v, want %v (±%v)", series, got, want, tol)
	}
}

func mustHave(t *testing.T, body string, want ...string) {
	t.Helper()
	for _, w := range want {
		if !hasLine(body, w) {
			t.Errorf("scrape missing line:\n  %s", w)
		}
	}
}

func TestCollectIdleMonitor(t *testing.T) {
	mon := monitor.New(63)
	body := scrape(t, mon)

	mustHave(t, body,
		"meater_up 1",
		"meater_discovery_running 0",
		"meater_probe_connected 0",
		"meater_probe_has_reading 0",
		`meater_state{state="idle"} 1`,
		`meater_state{state="cooking"} 0`,
		"meater_target_celsius 63",
		"meater_cook_samples 0",
	)

	// Without a reading the temperatures must be absent rather than a real 0 °C:
	// a dashboard showing a fridge-cold probe on a stopped service is worse than
	// showing nothing.
	mustHave(t, body,
		"meater_tip_celsius NaN",
		"meater_ambient_celsius NaN",
		"meater_eta_seconds NaN",
		"meater_last_sample_age_seconds NaN",
	)

	// The state enumeration must always cover every state exactly once.
	for _, st := range allStates {
		if !strings.Contains(body, `meater_state{state="`+st+`"}`) {
			t.Errorf("scrape missing state series %q", st)
		}
	}
}

func TestCollectCookingMonitor(t *testing.T) {
	mon := monitor.New(80)
	mon.Start()
	mon.SetCookName("brisket")
	mon.SetMeatType("beef brisket")
	mon.Update(meater.Reading{TipCelsius: 20, AmbientCelsius: 110})
	mon.Update(meater.Reading{TipCelsius: 30, AmbientCelsius: 110})

	body := scrape(t, mon)

	mustHave(t, body,
		"meater_discovery_running 1",
		"meater_probe_connected 1",
		"meater_probe_has_reading 1",
		"meater_tip_celsius 30",
		"meater_tip_fahrenheit 86",
		"meater_ambient_celsius 110",
		"meater_target_celsius 80",
		"meater_cook_samples 2",
		"meater_cook_start_tip_celsius 20",
		"meater_cook_max_tip_celsius 30",
		"meater_cook_max_ambient_celsius 110",
		"meater_remaining_celsius 50",
		`meater_cook_info{cook_id="0",meat_type="beef brisket",name="brisket"} 1`,
	)

	// Progress is measured from where the cook started, not from 0 °C: the tip
	// has climbed 10 of the 60 degrees to target.
	closeTo(t, body, "meater_cook_progress_ratio", 10.0/60.0, 0.001)

	// The source enumeration must always sum to exactly one active series.
	var active int
	for _, src := range allETASources {
		if hasLine(body, `meater_eta_source{source="`+src+`"} 1`) {
			active++
		}
	}
	if active != 1 {
		t.Errorf("active meater_eta_source series = %d, want 1", active)
	}
}

func TestCollectReadyClampsProgressAndRemaining(t *testing.T) {
	mon := monitor.New(50)
	mon.Start()
	// Overshooting the target must not report >100% progress or a negative gap.
	mon.Update(meater.Reading{TipCelsius: 20, AmbientCelsius: 100})
	mon.Update(meater.Reading{TipCelsius: 60, AmbientCelsius: 100})

	body := scrape(t, mon)
	mustHave(t, body,
		`meater_state{state="ready"} 1`,
		"meater_remaining_celsius 0",
		"meater_eta_seconds 0",
	)
	closeTo(t, body, "meater_cook_progress_ratio", 1, 0.001)
}

func TestGoAndProcessCollectorsRegistered(t *testing.T) {
	// A single scrape should cover the service's own health too, so an operator
	// does not need a second exporter beside it.
	body := scrape(t, monitor.New(63))
	for _, want := range []string{"go_goroutines", "go_memstats_alloc_bytes"} {
		if !strings.Contains(body, want) {
			t.Errorf("scrape missing runtime metric %q", want)
		}
	}
}
