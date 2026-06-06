package meater

import (
	"math"
	"testing"
)

func TestParseTemperatureTooShort(t *testing.T) {
	if _, err := ParseTemperature([]byte{0x01, 0x02}); err == nil {
		t.Fatal("expected error for short payload, got nil")
	}
}

func TestParseTemperatureDecodesTipAndAmbient(t *testing.T) {
	// internal raw 2121 -> (2121+8)/32 = 66.53; ambient raw 2813 (data[10:12])
	// -> (2813+8)/32 = 88.16.
	data := []byte{
		0x49, 0x08, // internal = 2121
		0x5d, 0x08, // sensor 2 (unused)
		0x65, 0x08, // sensor 3 (unused)
		0x74, 0x08, // sensor 4 (unused)
		0x77, 0x08, // sensor 5 (unused)
		0xfd, 0x0a, // ambient  = 2813
	}

	got, err := ParseTemperature(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !approxEqual(got.TipCelsius, 66.53125) {
		t.Errorf("TipCelsius = %v, want 66.53125", got.TipCelsius)
	}
	if !approxEqual(got.AmbientCelsius, 88.15625) {
		t.Errorf("AmbientCelsius = %v, want 88.15625", got.AmbientCelsius)
	}
}

func TestParseTemperatureReachesCookTarget(t *testing.T) {
	// A 95C pork target is internal raw 3032 with the /32 scale; verify the
	// internal channel spans the full cooking range rather than saturating.
	data := []byte{0xd8, 0x0b, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00} // 0x0bd8 = 3032
	got, err := ParseTemperature(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approxEqual(got.TipCelsius, 95.0) {
		t.Errorf("TipCelsius = %v, want 95.0", got.TipCelsius)
	}
}

func TestParseTemperatureRealPayload(t *testing.T) {
	// A real 12-byte MEATER+ sample. internal raw 2009 -> 63.03C; ambient is read
	// from data[10:12] = 0x0dda = 3546 -> (3546+8)/32 = 111.06C.
	data := []byte{0xd9, 0x07, 0xe5, 0x07, 0xec, 0x07, 0xfb, 0x07, 0x00, 0x08, 0xda, 0x0d}

	got, err := ParseTemperature(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !approxEqual(got.TipCelsius, 63.03125) {
		t.Errorf("TipCelsius = %v, want 63.03125", got.TipCelsius)
	}
	if !approxEqual(got.AmbientCelsius, 111.0625) {
		t.Errorf("AmbientCelsius = %v, want 111.0625", got.AmbientCelsius)
	}
}

func approxEqual(a, b float64) bool {
	return math.Abs(a-b) < 0.05
}
