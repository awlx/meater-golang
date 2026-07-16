//go:build !nobluetooth

// Local Bluetooth source: reach the probe through this host's own BLE adapter
// (CoreBluetooth on macOS, BlueZ on Linux, WinRT on Windows).
//
// Everything BLE-specific lives here so the whole stack can be compiled out
// with -tags nobluetooth, leaving -bridge and -mock usable on hosts that have
// no BLE adapter -- or where merely *linking* one is fatal. macOS aborts
// (SIGABRT) any long-lived process that links CoreBluetooth without a Bluetooth
// usage description in a signed app bundle, which otherwise kills this program
// a few hundred milliseconds after startup even when Bluetooth is never
// touched. Since -bridge deliberately puts the radio on a remote ESP32, a
// bridge user should not have to link a local BLE stack at all.
package main

import (
	"flag"
	"fmt"
	"log"
	"strings"
	"sync/atomic"
	"time"

	"github.com/awlx/meater-golang/internal/meater"
	"github.com/awlx/meater-golang/internal/monitor"
	"tinygo.org/x/bluetooth"
)

var adapter = bluetooth.DefaultAdapter

var (
	targetAddr = flag.String("addr", "",
		"connect to this BLE MAC address (e.g. AA:BB:CC:DD:EE:FF) instead of matching by name")
	scanWindow = flag.Duration("scan-window", 15*time.Second,
		"how long each scan attempt runs before retrying")
	overallTimeout = flag.Duration("timeout", 0,
		"give up after this long (0 = retry forever)")
	connectRetries = flag.Int("connect-retries", 3,
		"how many times to retry a failed connection before rescanning")
	connectTimeout = flag.Duration("connect-timeout", 25*time.Second,
		"abort a connection attempt that takes longer than this (BlueZ can hang)")
	btAdapter = flag.String("adapter", "",
		"Bluetooth adapter to use, as an hci id (e.g. hci1) or a controller MAC (e.g. AA:BB:CC:DD:EE:FF); empty uses hci0. A MAC is resolved to its hci id at startup so it survives adapter re-numbering across reboots")
)

// runBLE keeps a MEATER probe connected, decoding its temperature stream into
// the monitor and reconnecting whenever the link drops.
func runBLE(mon *monitor.Monitor) {
	if err := selectAdapter(*btAdapter); err != nil {
		log.Fatalf("select Bluetooth adapter %q: %v", *btAdapter, err)
	}
	must("enable BLE adapter", adapter.Enable())
	serviceUUID := mustUUID(meater.ServiceUUID)
	tempUUID := mustUUID(meater.TemperatureCharUUID)

	for {
		// Block until the user presses Start; nothing scans in the background.
		mon.WaitForStart()
		stop := mon.StopChan()

		conn, ok := findAndConnect(stop)
		if !ok {
			// Stopped before a probe was found.
			continue
		}
		mon.SetConnected(true)
		log.Println("connected, discovering services...")

		if streamUntilStale(conn, serviceUUID, tempUUID, mon, stop) {
			log.Println("stream stalled, reconnecting...")
		}
		_ = conn.Disconnect()
		mon.SetConnected(false)
	}
}

// streamUntilStale subscribes to the temperature characteristic and feeds the
// monitor until the stream goes silent or stop is closed. It returns true once
// stale (so the caller reconnects) and false when stopped by the user.
func streamUntilStale(conn bluetooth.Device, serviceUUID, tempUUID bluetooth.UUID, mon *monitor.Monitor, stop <-chan struct{}) bool {
	// Enumerate the whole GATT table rather than filtering by a hard-coded
	// service UUID: BlueZ resolves the services it sees over the air, and the
	// MEATER+ doesn't always advertise the service UUID we expect. We log what
	// is actually present and then locate the temperature characteristic by its
	// UUID wherever it lives.
	var services []bluetooth.DeviceService
	var err error
	for attempt := 1; attempt <= 5; attempt++ {
		services, err = conn.DiscoverServices(nil)
		if err == nil && len(services) > 0 {
			break
		}
		log.Printf("service discovery attempt %d/5 failed: %v", attempt, err)
		time.Sleep(2 * time.Second)
	}
	if err != nil || len(services) == 0 {
		log.Printf("no services resolved: %v", err)
		return true
	}

	var tempChar *bluetooth.DeviceCharacteristic
	for i := range services {
		svc := services[i]
		chars, cerr := svc.DiscoverCharacteristics(nil)
		if cerr != nil {
			log.Printf("characteristic discovery failed for %s: %v", svc.UUID().String(), cerr)
			continue
		}
		for j := range chars {
			if chars[j].UUID() == tempUUID {
				tempChar = &chars[j]
			}
		}
	}
	if tempChar == nil {
		log.Printf("temperature characteristic %s not found in GATT table", tempUUID.String())
		return true
	}

	var lastUpdate atomic.Int64
	lastUpdate.Store(time.Now().UnixNano())

	err = tempChar.EnableNotifications(func(buf []byte) {
		reading, err := meater.ParseTemperature(buf)
		if err != nil {
			log.Printf("decode error: %v", err)
			return
		}
		lastUpdate.Store(time.Now().UnixNano())
		mon.Update(reading)
	})
	if err != nil {
		log.Printf("enable notifications failed: %v", err)
		return true
	}

	log.Println("streaming temperatures")
	for {
		select {
		case <-stop:
			return false
		case <-time.After(5 * time.Second):
		}
		if time.Since(time.Unix(0, lastUpdate.Load())) > 20*time.Second {
			return true
		}
	}
}

// findAndConnect repeatedly scans for the probe and connects to it, retrying
// until it succeeds, the overall timeout elapses, or stop is closed. ok is
// false when discovery was stopped before a probe was connected.
func findAndConnect(stop <-chan struct{}) (bluetooth.Device, bool) {
	deadline := time.Time{}
	if *overallTimeout > 0 {
		deadline = time.Now().Add(*overallTimeout)
	}

	if *targetAddr != "" {
		log.Printf("looking for probe at address %s...", *targetAddr)
	} else {
		log.Printf("scanning for a %s* probe...", meater.NamePrefix)
	}

	for attempt := 1; ; attempt++ {
		select {
		case <-stop:
			return bluetooth.Device{}, false
		default:
		}

		// If a flaky USB controller re-enumerated (e.g. hci1 -> hci2), re-point
		// the backend at its new hci id before scanning so we don't talk to a
		// vanished D-Bus object. No-op for the default/hci-id selection.
		refreshAdapter()

		result, found, err := scanOnce(*scanWindow, stop)
		if err != nil {
			// BlueZ can report "Operation already in progress" if a previous
			// scan has not fully stopped yet, or a vanished-adapter error if the
			// controller just re-enumerated. Re-resolve and recover instead of
			// exiting.
			log.Printf("scan error: %v (recovering)", err)
			_ = adapter.StopScan()
			refreshAdapter()
			time.Sleep(2 * time.Second)
			continue
		}

		if found {
			log.Printf("found %q (%s), connecting...",
				displayName(result), result.Address.String())
			// On Linux, drive BlueZ directly to clear any stale half-open
			// connection and block until GATT services are resolved before
			// handing the device to the tinygo backend. No-op elsewhere.
			if err := ensureConnected(result.Address.String()); err != nil {
				log.Printf("bluez prepare failed: %v", err)
			}
			conn, err := connectWithRetries(result.Address)
			if err == nil {
				return conn, true
			}
			log.Printf("connection failed: %v", err)
		} else {
			log.Printf("no probe found on attempt %d", attempt)
		}

		// Bail promptly if the user pressed Stop during the attempt.
		select {
		case <-stop:
			return bluetooth.Device{}, false
		default:
		}

		if !deadline.IsZero() && time.Now().After(deadline) {
			log.Fatalf("gave up after %s (probe may be connected to another device)", *overallTimeout)
		}
		log.Println("retrying...")
	}
}

// scanOnce runs a single scan attempt for up to window, returning the first
// matching probe it sees. found is false if the window elapsed with no match.
// Closing stop aborts the scan early.
func scanOnce(window time.Duration, stop <-chan struct{}) (result bluetooth.ScanResult, found bool, err error) {
	// Clear any lingering discovery so BlueZ doesn't reject the new scan with
	// "Operation already in progress".
	_ = adapter.StopScan()
	time.Sleep(200 * time.Millisecond)

	timer := time.AfterFunc(window, func() { _ = adapter.StopScan() })
	defer timer.Stop()

	// Abort the scan immediately if the user presses Stop mid-window.
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-stop:
			_ = adapter.StopScan()
		case <-done:
		}
	}()

	err = adapter.Scan(func(a *bluetooth.Adapter, r bluetooth.ScanResult) {
		if !matches(r) {
			return
		}
		result = r
		found = true
		_ = a.StopScan()
	})
	return result, found, err
}

// matches reports whether a scan result is the probe we are looking for,
// either by an explicit target address or by the advertised name prefix.
func matches(r bluetooth.ScanResult) bool {
	if *targetAddr != "" {
		return strings.EqualFold(r.Address.String(), *targetAddr)
	}
	return meater.IsProbeName(r.LocalName())
}

// connectWithRetries attempts to connect a few times before giving up. It makes
// sure scanning has stopped first, since connecting while a scan is still
// active makes BlueZ abort with le-connection-abort-by-local.
func connectWithRetries(addr bluetooth.Address) (bluetooth.Device, error) {
	var lastErr error
	for i := 1; i <= *connectRetries; i++ {
		_ = adapter.StopScan()
		time.Sleep(300 * time.Millisecond)

		conn, err := connectWithTimeout(addr, *connectTimeout)
		if err == nil {
			return conn, nil
		}
		lastErr = err
		log.Printf("connect attempt %d/%d failed: %v", i, *connectRetries, err)
		time.Sleep(time.Second)
	}
	return bluetooth.Device{}, lastErr
}

// connectWithTimeout wraps adapter.Connect with a hard timeout. On Linux the
// underlying BlueZ connect can block forever when it misses the "Connected"
// D-Bus signal (common on a weak link), so we bound it ourselves and clean up
// any connection that completes after we have already given up.
func connectWithTimeout(addr bluetooth.Address, timeout time.Duration) (bluetooth.Device, error) {
	type result struct {
		dev bluetooth.Device
		err error
	}
	ch := make(chan result, 1)
	go func() {
		dev, err := adapter.Connect(addr, bluetooth.ConnectionParams{
			ConnectionTimeout: bluetooth.NewDuration(timeout),
		})
		ch <- result{dev, err}
	}()

	select {
	case r := <-ch:
		return r.dev, r.err
	case <-time.After(timeout):
		go func() {
			if r := <-ch; r.err == nil {
				_ = r.dev.Disconnect()
			}
		}()
		return bluetooth.Device{}, fmt.Errorf("connect timed out after %s", timeout)
	}
}

// displayName returns the advertised name, falling back to the address when
// the probe does not include its name in the advertisement.
func displayName(r bluetooth.ScanResult) string {
	if name := r.LocalName(); name != "" {
		return name
	}
	return r.Address.String()
}

func mustUUID(s string) bluetooth.UUID {
	uuid, err := bluetooth.ParseUUID(s)
	must("parse UUID "+s, err)
	return uuid
}

func must(action string, err error) {
	if err != nil {
		log.Fatalf("%s: %v", action, err)
	}
}
