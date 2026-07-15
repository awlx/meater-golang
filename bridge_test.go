package main

import "testing"

// The bridge forwards the probe's raw payload untouched so that
// meater.ParseTemperature stays the only decoder in the project. This test
// pins that contract to the payload documented in internal/meater/meater.go as
// validated against the official MEATER app (tip raw 2121, ambient raw 2813):
// if the firmware ever starts pre-decoding, or the hex framing changes, the
// numbers move and this fails.
func TestDecodeBridgeTempMatchesValidatedPayload(t *testing.T) {
	// byte 0..1 = 49 08 -> 0x0849 = 2121 -> (2121+8)/32 = 66.53C
	// byte 10..11 = fd 0a -> 0x0afd = 2813 -> (2813+8)/32 = 88.16C
	const payload = "49080000000000000000fd0a"

	got, err := decodeBridgeTemp(payload)
	if err != nil {
		t.Fatalf("decodeBridgeTemp(%q) returned error: %v", payload, err)
	}

	const tolerance = 0.05
	if diff := got.TipCelsius - 66.53125; diff > tolerance || diff < -tolerance {
		t.Errorf("TipCelsius = %v, want ~66.53", got.TipCelsius)
	}
	if diff := got.AmbientCelsius - 88.15625; diff > tolerance || diff < -tolerance {
		t.Errorf("AmbientCelsius = %v, want ~88.16", got.AmbientCelsius)
	}
}

func TestDecodeBridgeTempRejectsBadInput(t *testing.T) {
	tests := []struct {
		name    string
		payload string
	}{
		{"not hex", "zzzz"},
		{"odd length", "4908000"},
		{"too short for a reading", "4908"}, // 2 bytes; decoder wants >= 4
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := decodeBridgeTemp(tt.payload); err == nil {
				t.Errorf("decodeBridgeTemp(%q) = nil error, want error", tt.payload)
			}
		})
	}
}
