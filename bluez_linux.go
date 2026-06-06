//go:build linux

package main

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/godbus/dbus/v5"
)

// ensureConnected uses BlueZ directly over D-Bus to establish a fully
// service-resolved GATT connection to the probe.
//
// The tinygo Linux backend is passive: its DiscoverServices only polls BlueZ's
// ServicesResolved property, and its Connect short-circuits when BlueZ already
// reports Connected=true. On a real cook we kept hitting a stale, half-open
// connection (Connected=true but ServicesResolved=false) that never resolved
// and was then torn down. BlueZ's own Device1.Connect method, by contrast,
// blocks until the link is up AND the GATT database has been browsed, so we use
// it here and verify ServicesResolved before handing back to tinygo.
func ensureConnected(mac string) error {
	bus, err := dbus.SystemBus()
	if err != nil {
		return fmt.Errorf("system bus: %w", err)
	}

	path := dbus.ObjectPath("/org/bluez/hci0/dev_" +
		strings.ReplaceAll(strings.ToUpper(mac), ":", "_"))
	dev := bus.Object("org.bluez", path)

	// Trust the device so BlueZ is willing to keep/auto-restore the link.
	_ = dev.SetProperty("org.bluez.Device1.Trusted", dbus.MakeVariant(true))

	// Clear a stale half-open connection: connected but never resolved. Leaving
	// it in place makes tinygo's Connect return instantly without ever getting
	// a usable GATT table.
	if connected, _ := boolProp(dev, "org.bluez.Device1.Connected"); connected {
		if resolved, _ := boolProp(dev, "org.bluez.Device1.ServicesResolved"); !resolved {
			log.Println("clearing stale half-open connection...")
			_ = dev.Call("org.bluez.Device1.Disconnect", 0).Err
			waitProp(dev, "org.bluez.Device1.Connected", false, 5*time.Second)
		} else {
			return nil // already connected and resolved
		}
	}

	// Connect. BlueZ blocks here until services are resolved or it fails.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if call := dev.CallWithContext(ctx, "org.bluez.Device1.Connect", 0); call.Err != nil {
		return fmt.Errorf("bluez connect: %w", call.Err)
	}

	if !waitProp(dev, "org.bluez.Device1.ServicesResolved", true, 20*time.Second) {
		return fmt.Errorf("services did not resolve")
	}
	log.Println("bluez: services resolved")
	return nil
}

func boolProp(obj dbus.BusObject, name string) (bool, error) {
	v, err := obj.GetProperty(name)
	if err != nil {
		return false, err
	}
	b, _ := v.Value().(bool)
	return b, nil
}

func waitProp(obj dbus.BusObject, name string, want bool, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if b, err := boolProp(obj, name); err == nil && b == want {
			return true
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
