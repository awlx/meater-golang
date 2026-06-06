//go:build !linux

package main

// ensureConnected is a no-op outside Linux/BlueZ; CoreBluetooth and the Windows
// backend resolve services as part of their own connect path.
func ensureConnected(mac string) error { return nil }
