// Package meater decodes the BLE temperature payload broadcast by the
// MEATER wireless meat thermometer probe.
package meater

import (
	"fmt"
	"strings"
)

// BLE identifiers exposed by the MEATER probe.
const (
	// NamePrefix is the prefix of the local name the probe advertises while
	// scanning. It covers both the original "MEATER" and the long-range
	// "MEATER+" (and "MEATER Block") variants.
	NamePrefix = "MEATER"

	// ServiceUUID is the primary service that carries probe telemetry.
	ServiceUUID = "c9e2746c-59f1-4e54-a0dd-e1e54555cf8b"

	// TemperatureCharUUID streams the raw tip/ambient temperature payload.
	TemperatureCharUUID = "7edda774-045e-4bbf-909b-45d1991a2876"

	// BatteryCharUUID reports the probe battery level (0-100 scaled).
	BatteryCharUUID = "2adb4877-68d8-4884-bd3c-d83853bf27b8"
)

// IsProbeName reports whether an advertised BLE local name belongs to a
// MEATER probe (e.g. "MEATER", "MEATER+", "MEATER Block").
func IsProbeName(name string) bool {
	return strings.HasPrefix(name, NamePrefix)
}

// Reading is a single decoded temperature sample from the probe.
type Reading struct {
	// TipCelsius is the internal (meat) temperature at the probe tip.
	TipCelsius float64
	// AmbientCelsius is the ambient (cook/grill) temperature.
	AmbientCelsius float64
}

// TipFahrenheit returns the tip temperature in degrees Fahrenheit.
func (r Reading) TipFahrenheit() float64 { return celsiusToFahrenheit(r.TipCelsius) }

// AmbientFahrenheit returns the ambient temperature in degrees Fahrenheit.
func (r Reading) AmbientFahrenheit() float64 { return celsiusToFahrenheit(r.AmbientCelsius) }

// ParseTemperature decodes the raw bytes from the temperature characteristic.
//
// The payload is a little-endian sequence of uint16 sensor values. This 12-byte
// "resolution 32" (v2) MEATER+ firmware reports several full per-sensor
// temperatures rather than the small offsets assumed by the documented 8-byte
// decoding, so every channel converts with the same /32 scale.
//
//   - data[0:2]   internal (meat tip) sensor
//   - data[10:12] ambient (cook) sensor
//
// The /32 scale on the internal channel is what lets it span the full cooking
// range (a 95C target is raw 3032) instead of saturating near 64C, and matches
// the probe's own readout. The ambient sensor lives at offset 10 (NOT data[2:4],
// which sits in the same range as the tip): it is the channel that visibly swings
// with the cook temperature while the tip rises slowly. Both were validated
// against the official app, e.g. internal 66.1C / ambient ~88C for a payload of
// 49 08 ... fd 0a (raw 2121 -> 66.5C tip, raw 2813 -> 88.2C ambient).
func ParseTemperature(data []byte) (Reading, error) {
	if len(data) < 4 {
		return Reading{}, fmt.Errorf("meater: temperature payload too short: got %d bytes, want >= 4", len(data))
	}

	internal := readUint16LE(data, 0)
	reading := Reading{
		TipCelsius: (float64(internal) + 8.0) / 32.0,
	}

	// The ambient sensor is reported at byte offset 10 on the 12-byte firmware.
	if len(data) >= 12 {
		ambient := readUint16LE(data, 10)
		reading.AmbientCelsius = (float64(ambient) + 8.0) / 32.0
	}

	return reading, nil
}

// readUint16LE reads a little-endian uint16 at the given byte offset.
func readUint16LE(b []byte, offset int) int {
	return int(b[offset]) | int(b[offset+1])<<8
}

func celsiusToFahrenheit(c float64) float64 {
	return c*9.0/5.0 + 32.0
}
