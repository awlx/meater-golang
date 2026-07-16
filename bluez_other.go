//go:build !linux

package main

// ensureConnected is a no-op outside Linux/BlueZ; CoreBluetooth and the Windows
// backend resolve services as part of their own connect path.
func ensureConnected(mac string) error { return nil }

// selectAdapter is a no-op outside Linux; adapter selection by hci id / MAC is
// a BlueZ-specific concept.
func selectAdapter(spec string) error { return nil }

// refreshAdapter is a no-op outside Linux; only the BlueZ backend re-enumerates
// controllers by hci id.
func refreshAdapter() {}
