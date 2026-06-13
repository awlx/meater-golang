package monitor

import (
	"math"
	"testing"
)

// perSec converts a Celsius-per-minute rate to the per-second rate etaSeconds
// expects.
func perSec(perMin float64) float64 { return perMin / 60 }

func TestETASecondsLongerThanLinear(t *testing.T) {
	// A typical low-and-slow smoke: chamber well above the target. The
	// exponential (Newton cooling) estimate must be meaningfully longer than a
	// naive straight-line extrapolation, which is the whole point of the fix.
	tip, target, ambient, rate := 24.3, 95.0, 118.6, perSec(0.51)

	linear := (target - tip) / rate
	got := etaSeconds(tip, target, ambient, rate)

	if got <= linear {
		t.Fatalf("expected exponential ETA %.0fs to exceed linear ETA %.0fs", got, linear)
	}
	// Sanity: still finite and positive.
	if got <= 0 || math.IsInf(got, 0) || math.IsNaN(got) {
		t.Fatalf("ETA not a sensible positive duration: %v", got)
	}
}

func TestETASecondsDecreasesAsTipApproachesTarget(t *testing.T) {
	target, ambient, rate := 95.0, 118.6, perSec(0.5)

	early := etaSeconds(30, target, ambient, rate)
	late := etaSeconds(85, target, ambient, rate)

	if !(late < early) {
		t.Fatalf("expected ETA to shrink as tip nears target: early=%.0f late=%.0f", early, late)
	}
}

func TestETASecondsUnknownWhenNotRising(t *testing.T) {
	if got := etaSeconds(40, 95, 120, 0); got != -1 {
		t.Fatalf("zero rate should be unknown (-1), got %v", got)
	}
	if got := etaSeconds(96, 95, 120, perSec(0.5)); got != -1 {
		t.Fatalf("tip already at/above target should be unknown (-1), got %v", got)
	}
}

func TestETASecondsFallsBackToLinearWhenChamberNotHotter(t *testing.T) {
	// Chamber not meaningfully above the target: the exponential model does not
	// apply, so it falls back to a straight line rather than returning nonsense.
	tip, target, ambient, rate := 80.0, 95.0, 90.0, perSec(0.5)

	want := (target - tip) / rate
	got := etaSeconds(tip, target, ambient, rate)

	if math.Abs(got-want) > 1e-6 {
		t.Fatalf("expected linear fallback %.3f, got %.3f", want, got)
	}
}

func TestETASecondsUnknownDuringDeepStall(t *testing.T) {
	// Mid-stall: the tip crawls at a tiny rate (here 0.005 °C/min). The model
	// would otherwise extrapolate to days; it must report unknown (-1) once the
	// estimate exceeds the cap instead of showing an absurd number.
	tip, target, ambient, rate := 72.0, 95.0, 120.0, perSec(0.005)

	got := etaSeconds(tip, target, ambient, rate)
	if got != -1 {
		t.Fatalf("deep stall should be unknown (-1), got %.0fs (cap %ds)", got, etaMaxSeconds)
	}
}

func TestETASecondsWithinCapStaysKnown(t *testing.T) {
	// A normal low-and-slow rate stays a real, finite estimate under the cap.
	tip, target, ambient, rate := 70.0, 95.0, 120.0, perSec(0.3)

	got := etaSeconds(tip, target, ambient, rate)
	if got <= 0 || got > etaMaxSeconds {
		t.Fatalf("expected a finite ETA within the cap, got %.0fs", got)
	}
}
