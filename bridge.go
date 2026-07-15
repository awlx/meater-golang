// Bridge source: read the probe through a networked ESP32 BLE bridge instead of
// a local Bluetooth adapter.
//
// Microcontrollers such as the Olimex ESP32-POE-ISO cannot run this program (no
// OS, and the SQLite/BlueZ/net-http dependencies need a full POSIX host), but
// they make excellent PoE-powered BLE radios: the board sits in range of the
// grill on an Ethernet drop while this program runs on a real host elsewhere.
//
// The bridge firmware forwards the probe's *raw* characteristic payload rather
// than decoded temperatures, so meater.ParseTemperature stays the single source
// of truth for the wire format and its calibration.
package main

import (
	"bufio"
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/awlx/meater-golang/internal/meater"
	"github.com/awlx/meater-golang/internal/monitor"
)

// Bridge wire protocol (one ASCII line per message, \n terminated):
//
//	T <hex>          raw temperature characteristic payload, hex encoded
//	S connected      the bridge has a live GATT link to the probe
//	S disconnected   the bridge lost the probe (it keeps rescanning)
//	# <text>         human-readable banner/log, ignored
//
// This program is the TCP *client*: it dials the bridge when the user presses
// Start and hangs up on Stop. The firmware scans for the probe only while a
// client is attached, which maps the existing Start/Stop contract onto the
// remote radio without any extra control channel.
const (
	bridgeTempPrefix   = "T "
	bridgeStatusPrefix = "S "
)

// Link timings. Vars rather than consts so tests can shorten them; nothing
// outside tests should assign to them.
var (
	// bridgeStaleAfter bounds silence on the *link*, not gaps between readings.
	//
	// The distinction matters: a probe that isn't in range yet produces no
	// readings for minutes at a time, which is normal, and tearing the socket
	// down over it would abort the scan the bridge is running on our behalf and
	// livelock in redials. So the bridge instead guarantees a line at least
	// every ~10s -- a reading, a status change, or a "#" keepalive while it
	// scans -- and any of them proves the board is alive. Silence this long
	// means the board or the network is wedged, which redialing can actually
	// fix.
	bridgeStaleAfter = 30 * time.Second

	// bridgeWatchdogTick is how often that silence is checked.
	bridgeWatchdogTick = 5 * time.Second

	// bridgeRedialDelay paces reconnect attempts when the board is rebooting,
	// being reflashed, or simply not powered yet.
	bridgeRedialDelay = 3 * time.Second

	// bridgeDialTimeout bounds a single dial so a black-holed IP (unplugged
	// PoE, wrong address) fails fast enough to log and retry.
	bridgeDialTimeout = 10 * time.Second
)

// runBridge keeps a MEATER probe streaming through a remote ESP32 bridge,
// decoding its temperature stream into the monitor and redialing whenever the
// link drops. It is a drop-in peer of runBLE and runMock: same WaitForStart /
// SetConnected / Update contract, different transport.
func runBridge(mon *monitor.Monitor) {
	for {
		// Block until the user presses Start; nothing dials in the background.
		mon.WaitForStart()
		stop := mon.StopChan()

		conn, ok := dialBridge(stop)
		if !ok {
			// Stopped before the bridge answered.
			continue
		}

		if streamBridge(conn, mon, stop) {
			log.Println("bridge stream stalled, reconnecting...")
		}
		_ = conn.Close()
		mon.SetConnected(false)
	}
}

// dialBridge repeatedly dials the bridge until it answers or stop is closed.
// ok is false when the user pressed Stop before a connection was established.
func dialBridge(stop <-chan struct{}) (net.Conn, bool) {
	log.Printf("connecting to ESP32 bridge at %s...", *bridgeAddr)

	for attempt := 1; ; attempt++ {
		select {
		case <-stop:
			return nil, false
		default:
		}

		conn, err := net.DialTimeout("tcp", *bridgeAddr, bridgeDialTimeout)
		if err == nil {
			log.Printf("bridge connected (%s)", conn.RemoteAddr())
			return conn, true
		}
		log.Printf("bridge dial attempt %d failed: %v", attempt, err)

		select {
		case <-stop:
			return nil, false
		case <-time.After(bridgeRedialDelay):
		}
	}
}

// streamBridge reads the bridge's line protocol and feeds the monitor until the
// probe goes silent, the socket errors, or stop is closed. It returns true once
// stale or broken (so the caller redials) and false when stopped by the user.
func streamBridge(conn net.Conn, mon *monitor.Monitor, stop <-chan struct{}) bool {
	var lastUpdate atomic.Int64
	lastUpdate.Store(time.Now().UnixNano())

	// Closing the socket is what unblocks the blocking Scan below, so the
	// watchdog owns both the Stop signal and staleness detection.
	stalled := make(chan bool, 1)
	done := make(chan struct{})
	defer close(done)
	go func() {
		for {
			select {
			case <-stop:
				stalled <- false
				_ = conn.Close()
				return
			case <-done:
				return
			case <-time.After(bridgeWatchdogTick):
				if time.Since(time.Unix(0, lastUpdate.Load())) > bridgeStaleAfter {
					stalled <- true
					_ = conn.Close()
					return
				}
			}
		}
	}()

	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())

		// Any line at all proves the board is still alive and working, so it
		// resets the watchdog -- including the keepalives it sends while
		// scanning for an absent probe.
		lastUpdate.Store(time.Now().UnixNano())

		switch {
		case strings.HasPrefix(line, bridgeTempPrefix):
			reading, err := decodeBridgeTemp(strings.TrimPrefix(line, bridgeTempPrefix))
			if err != nil {
				log.Printf("bridge decode error: %v", err)
				continue
			}
			mon.Update(reading)

		case strings.HasPrefix(line, bridgeStatusPrefix):
			switch status := strings.TrimPrefix(line, bridgeStatusPrefix); status {
			case "connected":
				log.Println("bridge reports probe connected, streaming temperatures")
				mon.SetConnected(true)
			case "disconnected":
				log.Println("bridge reports probe not connected, it will keep scanning")
				mon.SetConnected(false)
			default:
				log.Printf("bridge: unknown status %q", status)
			}

		case line == "" || strings.HasPrefix(line, "#"):
			// Banner or scan keepalive; its only job was to reset the watchdog.

		default:
			log.Printf("bridge: unrecognised line %q", line)
		}
	}

	// The watchdog closed the socket: report why so the caller can redial or
	// return to idle.
	select {
	case wasStale := <-stalled:
		return wasStale
	default:
	}

	if err := scanner.Err(); err != nil {
		log.Printf("bridge read error: %v", err)
	} else {
		log.Println("bridge closed the connection")
	}
	return true
}

// decodeBridgeTemp turns a hex-encoded raw characteristic payload back into the
// exact bytes the probe sent, then hands them to the shared decoder so the
// bridge path and the local BLE path can never drift apart.
func decodeBridgeTemp(payload string) (meater.Reading, error) {
	raw, err := hex.DecodeString(payload)
	if err != nil {
		return meater.Reading{}, fmt.Errorf("bad hex payload %q: %w", payload, err)
	}
	return meater.ParseTemperature(raw)
}
