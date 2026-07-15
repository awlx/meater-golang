package monitor

import (
	"testing"

	"github.com/awlx/meater-golang/internal/meater"
)

func TestProgressMeasuresFromCookStart(t *testing.T) {
	m := New(80)
	m.Start()
	// A cook that began at 20 °C and is now at 50 °C is half way to an 80 °C
	// target — not 62.5%, which is what tip/target would claim.
	m.Update(meater.Reading{TipCelsius: 20, AmbientCelsius: 150})
	m.Update(meater.Reading{TipCelsius: 50, AmbientCelsius: 150})

	s := m.Status()
	if got, want := s.ProgressPercent, 50.0; got != want {
		t.Errorf("ProgressPercent = %v, want %v", got, want)
	}
	if got, want := s.StartTipCelsius, 20.0; got != want {
		t.Errorf("StartTipCelsius = %v, want %v", got, want)
	}
}

func TestProgressClampedToTarget(t *testing.T) {
	m := New(50)
	m.Start()
	m.Update(meater.Reading{TipCelsius: 20, AmbientCelsius: 150})
	// Overshooting must report 100%, never more: a progress bar past its own end
	// is worse than one that simply says "done".
	m.Update(meater.Reading{TipCelsius: 70, AmbientCelsius: 150})

	if got := m.Status().ProgressPercent; got != 100 {
		t.Errorf("ProgressPercent on overshoot = %v, want 100", got)
	}
}

func TestProgressUnknownWithoutAClimb(t *testing.T) {
	// A probe already at or above its target when the cook opened has no climb
	// to measure, so progress must report unknown rather than a made-up number.
	m := New(50)
	m.Start()
	m.Update(meater.Reading{TipCelsius: 60, AmbientCelsius: 150})

	if got := m.Status().ProgressPercent; got != -1 {
		t.Errorf("ProgressPercent with no climb = %v, want -1", got)
	}
}

func TestProgressUnknownBeforeAnyReading(t *testing.T) {
	m := New(80)
	m.Start()
	if got := m.Status().ProgressPercent; got != -1 {
		t.Errorf("ProgressPercent before a reading = %v, want -1", got)
	}
	if got := m.Status().ElapsedSeconds; got != -1 {
		t.Errorf("ElapsedSeconds before a reading = %v, want -1", got)
	}
}
