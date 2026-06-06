// Command meater connects to a MEATER Bluetooth LE probe and serves a live web
// UI and JSON API showing tip/ambient temperatures and an estimated cooking
// time. Use -mock to run the UI without a probe.
package main

import (
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/awlx/meater-golang/internal/meater"
	"github.com/awlx/meater-golang/internal/monitor"
	"github.com/awlx/meater-golang/internal/server"
	"golang.org/x/crypto/acme/autocert"
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
	httpAddr = flag.String("http", ":8080",
		"address for the plain-HTTP server (also serves ACME HTTP-01 challenges and redirects to HTTPS when TLS is enabled)")
	httpsAddr = flag.String("https", ":8443",
		"address for the HTTPS server (used when -acme-domain or -tls-cert/-tls-key are set)")
	acmeDomain = flag.String("acme-domain", "",
		"obtain a Let's Encrypt certificate automatically for this domain (e.g. meater.example.com); requires -http reachable on the public port 80 or -https on 443")
	acmeCache = flag.String("acme-cache", "acme-certs",
		"directory to cache ACME certificates in")
	tlsCert = flag.String("tls-cert", "",
		"path to a TLS certificate file (use with -tls-key for a cert from your own ACME client)")
	tlsKey = flag.String("tls-key", "",
		"path to a TLS private key file (use with -tls-cert)")
	targetTemp = flag.Float64("target", 63,
		"default target tip temperature in Celsius")
	mock = flag.Bool("mock", false,
		"simulate a probe instead of using Bluetooth (for UI testing)")
)

func main() {
	log.SetFlags(log.Ltime)
	flag.Parse()

	mon := monitor.New(*targetTemp)
	srv := server.New(mon)

	go startServer(srv.Handler())

	if *mock {
		log.Println("running in MOCK mode (no Bluetooth)")
		go runMock(mon)
	} else {
		go runBLE(mon)
	}

	waitForSignal()
	log.Println("shutting down")
}

// startServer serves the web UI/API. It chooses one of three modes:
//
//   - automatic ACME (Let's Encrypt) when -acme-domain is set,
//   - a static certificate when -tls-cert/-tls-key are set,
//   - plain HTTP otherwise.
//
// In the TLS modes the plain-HTTP listener (-http) is kept alive to serve ACME
// HTTP-01 challenges and to redirect browsers to HTTPS.
func startServer(handler http.Handler) {
	switch {
	case *acmeDomain != "":
		m := &autocert.Manager{
			Prompt:     autocert.AcceptTOS,
			HostPolicy: autocert.HostWhitelist(*acmeDomain),
			Cache:      autocert.DirCache(*acmeCache),
		}
		go func() {
			log.Printf("ACME HTTP-01 / redirect listener: %s", *httpAddr)
			if err := http.ListenAndServe(*httpAddr, m.HTTPHandler(redirectToHTTPS(*httpsAddr))); err != nil {
				log.Fatalf("http server: %v", err)
			}
		}()
		s := &http.Server{Addr: *httpsAddr, Handler: handler, TLSConfig: m.TLSConfig()}
		log.Printf("web UI: https://%s%s", *acmeDomain, normalizePort(*httpsAddr))
		if err := s.ListenAndServeTLS("", ""); err != nil {
			log.Fatalf("https server: %v", err)
		}

	case *tlsCert != "" && *tlsKey != "":
		go func() {
			log.Printf("HTTP redirect listener: %s", *httpAddr)
			if err := http.ListenAndServe(*httpAddr, redirectToHTTPS(*httpsAddr)); err != nil {
				log.Fatalf("http server: %v", err)
			}
		}()
		log.Printf("web UI: https://localhost%s", *httpsAddr)
		if err := http.ListenAndServeTLS(*httpsAddr, *tlsCert, *tlsKey, handler); err != nil {
			log.Fatalf("https server: %v", err)
		}

	default:
		log.Printf("web UI: http://localhost%s", *httpAddr)
		if err := http.ListenAndServe(*httpAddr, handler); err != nil {
			log.Fatalf("http server: %v", err)
		}
	}
}

// redirectToHTTPS sends every plain-HTTP request to the same host over HTTPS.
func redirectToHTTPS(httpsAddr string) http.Handler {
	port := normalizePort(httpsAddr)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if i := strings.IndexByte(host, ':'); i >= 0 {
			host = host[:i]
		}
		target := "https://" + host + port + r.URL.RequestURI()
		http.Redirect(w, r, target, http.StatusMovedPermanently)
	})
}

// normalizePort returns the ":port" suffix to show in URLs, hiding the
// well-known 443 so links read cleanly.
func normalizePort(addr string) string {
	if i := strings.LastIndexByte(addr, ':'); i >= 0 {
		if p := addr[i:]; p == ":443" {
			return ""
		}
		return addr[i:]
	}
	return ""
}

// runBLE keeps a MEATER probe connected, decoding its temperature stream into
// the monitor and reconnecting whenever the link drops.
func runBLE(mon *monitor.Monitor) {
	must("enable BLE adapter", adapter.Enable())
	serviceUUID := mustUUID(meater.ServiceUUID)
	tempUUID := mustUUID(meater.TemperatureCharUUID)

	for {
		conn := findAndConnect()
		mon.SetConnected(true)
		log.Println("connected, discovering services...")

		if streamUntilStale(conn, serviceUUID, tempUUID, mon) {
			log.Println("stream stalled, reconnecting...")
		}
		_ = conn.Disconnect()
		mon.SetConnected(false)
	}
}

// streamUntilStale subscribes to the temperature characteristic and feeds the
// monitor until the stream goes silent. It returns true once stale.
func streamUntilStale(conn bluetooth.Device, serviceUUID, tempUUID bluetooth.UUID, mon *monitor.Monitor) bool {
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
		time.Sleep(5 * time.Second)
		if time.Since(time.Unix(0, lastUpdate.Load())) > 20*time.Second {
			return true
		}
	}
}

// runMock simulates a cold roast warming toward a hot oven so the UI and ETA
// estimator can be exercised without hardware.
func runMock(mon *monitor.Monitor) {
	mon.SetConnected(true)
	const ambient = 110.0
	tip := 6.0
	for {
		tip += (ambient-tip)*0.02 + rand.Float64()*0.05
		if tip > ambient-1 {
			tip = ambient - 1
		}
		mon.Update(meater.Reading{
			TipCelsius:     tip,
			AmbientCelsius: ambient + (rand.Float64()-0.5)*3,
		})
		time.Sleep(time.Second)
	}
}

// findAndConnect repeatedly scans for the probe and connects to it, retrying
// until it succeeds or the overall timeout elapses.
func findAndConnect() bluetooth.Device {
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
		result, found, err := scanOnce(*scanWindow)
		if err != nil {
			// BlueZ can report "Operation already in progress" if a previous
			// scan has not fully stopped yet. Recover instead of exiting.
			log.Printf("scan error: %v (recovering)", err)
			_ = adapter.StopScan()
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
				return conn
			}
			log.Printf("connection failed: %v", err)
		} else {
			log.Printf("no probe found on attempt %d", attempt)
		}

		if !deadline.IsZero() && time.Now().After(deadline) {
			log.Fatalf("gave up after %s (probe may be connected to another device)", *overallTimeout)
		}
		log.Println("retrying...")
	}
}

// scanOnce runs a single scan attempt for up to window, returning the first
// matching probe it sees. found is false if the window elapsed with no match.
func scanOnce(window time.Duration) (result bluetooth.ScanResult, found bool, err error) {
	// Clear any lingering discovery so BlueZ doesn't reject the new scan with
	// "Operation already in progress".
	_ = adapter.StopScan()
	time.Sleep(200 * time.Millisecond)

	timer := time.AfterFunc(window, func() { _ = adapter.StopScan() })
	defer timer.Stop()

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

func waitForSignal() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
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
