//go:build linux

package main

import (
	"context"
	"fmt"
	"log"
	"reflect"
	"strings"
	"time"
	"unsafe"

	"github.com/godbus/dbus/v5"
)

// adapterID is the BlueZ controller (e.g. "hci0") this process drives. It is
// the default until selectAdapter picks another, and is used to build the
// per-adapter device object paths below.
var adapterID = "hci0"

// adapterSpec is the raw -adapter value (empty, an hci id, or a controller MAC)
// so refreshAdapter can re-resolve it if a flaky USB controller re-enumerates.
var adapterSpec string

// selectAdapter points both this BlueZ helper and the tinygo bluetooth backend
// at a specific controller. spec may be empty (keep the default hci0), an hci
// id like "hci1", or a controller MAC like "AA:BB:CC:DD:EE:FF" — a MAC is
// resolved to its current hci id so it keeps working even if USB re-enumeration
// renumbers the adapters across a reboot.
//
// tinygo's Linux backend hardcodes hci0 in an unexported field with no public
// selector, so we set that field directly; the field offset is pinned by the
// module version in go.mod.
func selectAdapter(spec string) error {
	spec = strings.TrimSpace(spec)
	adapterSpec = spec
	if spec == "" {
		return nil
	}

	id := spec
	if !isHciID(spec) {
		resolved, err := adapterIDForMAC(spec)
		if err != nil {
			return err
		}
		id = resolved
	}

	adapterID = id
	setTinygoAdapterID(id)
	log.Printf("using Bluetooth adapter %s", id)
	return nil
}

// refreshAdapter re-resolves a MAC-based -adapter selection to its current hci
// id and re-points the backend when it has changed. This self-heals the case
// where a flaky USB controller resets and re-enumerates mid-cook (e.g.
// hci1 -> hci2), which would otherwise leave the backend talking to a vanished
// D-Bus object ("SetDiscoveryFilter ... doesn't exist"). It is a no-op for an
// empty or hci-id selection, and treats a momentarily-missing controller as
// transient. Safe to call before each scan attempt.
func refreshAdapter() {
	if adapterSpec == "" || isHciID(adapterSpec) {
		return
	}
	id, err := adapterIDForMAC(adapterSpec)
	if err != nil || id == adapterID {
		return
	}
	log.Printf("Bluetooth adapter re-enumerated: %s -> %s", adapterID, id)
	adapterID = id
	setTinygoAdapterID(id)
	if err := adapter.Enable(); err != nil {
		log.Printf("re-enable adapter %s: %v", id, err)
	}
}

func isHciID(s string) bool {
	return strings.HasPrefix(strings.ToLower(s), "hci")
}

// adapterIDForMAC looks up the hci id of the controller with the given MAC via
// BlueZ's ObjectManager.
func adapterIDForMAC(mac string) (string, error) {
	bus, err := dbus.SystemBus()
	if err != nil {
		return "", fmt.Errorf("system bus: %w", err)
	}
	var managed map[dbus.ObjectPath]map[string]map[string]dbus.Variant
	err = bus.Object("org.bluez", "/").
		Call("org.freedesktop.DBus.ObjectManager.GetManagedObjects", 0).Store(&managed)
	if err != nil {
		return "", fmt.Errorf("list adapters: %w", err)
	}
	for path, ifaces := range managed {
		props, ok := ifaces["org.bluez.Adapter1"]
		if !ok {
			continue
		}
		addr, _ := props["Address"].Value().(string)
		if strings.EqualFold(addr, mac) {
			seg := string(path)
			if i := strings.LastIndex(seg, "/"); i >= 0 {
				seg = seg[i+1:]
			}
			return seg, nil
		}
	}
	return "", fmt.Errorf("no Bluetooth controller with address %s", mac)
}

// setTinygoAdapterID overwrites the unexported id field of the tinygo
// DefaultAdapter so its scans and connects use the chosen controller.
func setTinygoAdapterID(id string) {
	f := reflect.ValueOf(adapter).Elem().FieldByName("id")
	reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem().SetString(id)
}

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

	path := dbus.ObjectPath("/org/bluez/" + adapterID + "/dev_" +
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
