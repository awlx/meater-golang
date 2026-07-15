package main

import (
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/awlx/meater-golang/internal/monitor"
)

// shortenBridgeTimings makes the link watchdog fire in milliseconds instead of
// tens of seconds so the tests below stay fast.
func shortenBridgeTimings(t *testing.T, staleAfter, tick time.Duration) {
	t.Helper()
	oldStale, oldTick := bridgeStaleAfter, bridgeWatchdogTick
	bridgeStaleAfter, bridgeWatchdogTick = staleAfter, tick
	t.Cleanup(func() { bridgeStaleAfter, bridgeWatchdogTick = oldStale, oldTick })
}

// A probe that isn't in range yet is normal, not a failure: the bridge sends "#"
// keepalives while it scans, and they must hold the link open. Regression test
// for a livelock found on real hardware -- the watchdog used to measure time
// since the last *reading*, so with no probe present it tore down the socket
// mid-scan, redialled, and looped forever, never connecting even once the probe
// appeared.
func TestStreamBridgeKeepalivesHoldTheLinkOpen(t *testing.T) {
	shortenBridgeTimings(t, 150*time.Millisecond, 10*time.Millisecond)

	// The board talks for ~500ms -- over 3x bridgeStaleAfter -- without ever
	// sending a reading, exactly as it does while scanning for an absent probe.
	const keepalives = 20
	const interval = 25 * time.Millisecond
	const boardTalksFor = keepalives * interval

	client, board := net.Pipe()
	defer client.Close()

	go func() {
		defer board.Close()
		fmt.Fprint(board, "S disconnected\n")
		for i := 0; i < keepalives; i++ {
			if _, err := fmt.Fprint(board, "# scanning for a MEATER probe\n"); err != nil {
				return
			}
			time.Sleep(interval)
		}
	}()

	start := time.Now()
	done := make(chan bool, 1)
	go func() { done <- streamBridge(client, monitor.New(63), make(chan struct{})) }()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("streamBridge never returned")
	}
	elapsed := time.Since(start)

	// Both the fixed and the broken code eventually return true here, so the
	// return value proves nothing -- only the timing does. If keepalives don't
	// reset the watchdog it fires one bridgeStaleAfter in and hangs up early,
	// aborting the board's scan. It must instead last until the board itself
	// stops talking.
	if elapsed < boardTalksFor {
		t.Errorf("streamBridge hung up after %s, before the board stopped talking at %s: "+
			"keepalives are not resetting the %s link watchdog, so an absent probe livelocks",
			elapsed.Round(time.Millisecond), boardTalksFor, bridgeStaleAfter)
	}
}

// The flip side: total silence really is a wedged board, and must be redialled.
func TestStreamBridgeSilenceIsStale(t *testing.T) {
	shortenBridgeTimings(t, 100*time.Millisecond, 10*time.Millisecond)

	client, board := net.Pipe()
	defer client.Close()
	defer board.Close() // board says nothing at all

	done := make(chan bool, 1)
	go func() { done <- streamBridge(client, monitor.New(63), make(chan struct{})) }()

	select {
	case stale := <-done:
		if !stale {
			t.Error("streamBridge reported not-stale on a silent link; want stale so the caller redials")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("watchdog never fired on a silent link")
	}
}

// Pressing Stop must hang up rather than redial.
func TestStreamBridgeStopDoesNotRedial(t *testing.T) {
	shortenBridgeTimings(t, 5*time.Second, 10*time.Millisecond)

	client, board := net.Pipe()
	defer client.Close()
	defer board.Close()

	stop := make(chan struct{})
	done := make(chan bool, 1)
	go func() { done <- streamBridge(client, monitor.New(63), stop) }()

	time.Sleep(50 * time.Millisecond)
	close(stop)

	select {
	case stale := <-done:
		if stale {
			t.Error("streamBridge reported stale after Stop; want false so the caller returns to idle")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("streamBridge did not return after Stop")
	}
}

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
