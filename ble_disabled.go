//go:build nobluetooth

// Built with -tags nobluetooth: no local BLE stack is linked into the binary.
// The probe must be reached over the network with -bridge (an ESP32 BLE
// bridge), or simulated with -mock.
package main

import (
	"log"

	"github.com/awlx/meater-golang/internal/monitor"
)

// runBLE replaces the local Bluetooth source in nobluetooth builds. Reaching it
// means the user asked for local BLE from a binary that has none, so fail loudly
// with the two transports that do work rather than sitting silently idle.
func runBLE(mon *monitor.Monitor) {
	log.Fatal("this binary was built without local Bluetooth support (-tags nobluetooth): " +
		"use -bridge host:port to read the probe from an ESP32 bridge, or -mock to simulate one")
}
