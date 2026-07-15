// Command meater connects to a MEATER Bluetooth LE probe and serves a live web
// UI and JSON API showing tip/ambient temperatures and an estimated cooking
// time.
//
// The probe can be read from one of three interchangeable sources:
//
//   - a local Bluetooth adapter (the default; see ble.go),
//   - a networked ESP32 BLE bridge with -bridge (see bridge.go), for when the
//     grill is out of the host's Bluetooth range,
//   - a simulation with -mock, to run the UI without any hardware.
package main

import (
	"flag"
	"log"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/awlx/meater-golang/internal/meater"
	"github.com/awlx/meater-golang/internal/monitor"
	"github.com/awlx/meater-golang/internal/server"
	"github.com/awlx/meater-golang/internal/store"
	"golang.org/x/crypto/acme/autocert"
)

var (
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
	bridgeAddr = flag.String("bridge", "",
		"read the probe from a networked ESP32 BLE bridge at this host:port (e.g. meater-bridge.local:9000) instead of a local Bluetooth adapter")
	dbPath = flag.String("db", "meater.db",
		"path to the SQLite database for cook history (empty disables persistence)")
	cookIdle = flag.Duration("cook-idle", 30*time.Minute,
		"finish the current cook after this long without a reading (covers BLE drops/reconnects, so keep it well above a normal reconnect)")
)

func main() {
	log.SetFlags(log.Ltime)
	flag.Parse()

	mon := monitor.New(*targetTemp)

	var st *store.Store
	if *dbPath != "" {
		var err error
		st, err = store.Open(*dbPath)
		if err != nil {
			log.Printf("persistence disabled: open database %q: %v", *dbPath, err)
			st = nil
		} else {
			defer st.Close()
			mon.SetStore(st, *cookIdle)
			resumeCook(mon, st)
			mon.LoadHistory() // learn the time-to-target model from past cooks
			if err := st.Prune(); err != nil {
				log.Printf("prune cooks: %v", err)
			}
			go mon.RunJanitor()
		}
	}

	srv := server.New(mon, st)

	go startServer(srv.Handler())

	switch {
	case *mock:
		log.Println("running in MOCK mode (no Bluetooth)")
		go runMock(mon)
	case *bridgeAddr != "":
		log.Printf("reading probe via ESP32 bridge at %s (no local Bluetooth)", *bridgeAddr)
		go runBridge(mon)
	default:
		go runBLE(mon)
	}

	waitForSignal()
	log.Println("shutting down")
}

// resumeStaleAfter is how long an open cook may go without a reading before a
// restart treats it as a finished, separate session rather than resuming it. It
// is deliberately generous: a BLE wedge or a quick service restart routinely
// leaves a multi-minute gap mid-cook, and we must not split a long smoke (or
// stop scanning) over those. Only a cook that has been silent for many hours is
// assumed to be over.
const resumeStaleAfter = 12 * time.Hour

// resumeCook restores an in-progress cook on startup and re-enables probe
// discovery so the monitor reconnects on its own. A cook is resumed (its id,
// samples, and target kept) unless it has been idle longer than
// resumeStaleAfter, in which case it is finished and a fresh cook is started on
// the next reading. Either way discovery is turned back on so a restart never
// leaves the app idle in the middle of a cook.
func resumeCook(mon *monitor.Monitor, st *store.Store) {
	cook, err := st.CurrentOpenCook()
	if err != nil {
		log.Printf("resume cook: %v", err)
		return
	}
	if cook == nil {
		return
	}
	last, ok, err := st.LastSampleAt(cook.ID)
	if err != nil {
		log.Printf("resume cook: %v", err)
		return
	}
	if ok && time.Since(last) > resumeStaleAfter {
		if err := st.EndCook(cook.ID, last); err != nil {
			log.Printf("finish stale cook: %v", err)
		}
		mon.EnableDiscovery()
		log.Printf("previous cook #%d idle %s; starting fresh, scanning for probe",
			cook.ID, time.Since(last).Round(time.Minute))
		return
	}
	pts, err := st.CookSamples(cook.ID)
	if err != nil {
		log.Printf("resume cook samples: %v", err)
		mon.EnableDiscovery() // still scan even though history failed to load
		return
	}
	mpts := make([]monitor.Point, len(pts))
	for i, p := range pts {
		mpts[i] = monitor.Point{At: p.At, TipCelsius: p.TipCelsius, AmbientCelsius: p.AmbientCelsius}
	}
	mon.Resume(cook.ID, cook.Name, cook.MeatType, cook.TargetCelsius, mpts)
	log.Printf("resumed cook #%d %q with %d samples; scanning for probe", cook.ID, cook.Name, len(mpts))
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

// runMock simulates a cold roast warming toward a hot oven so the UI and ETA
// estimator can be exercised without hardware. Like the real BLE loop it only
// produces readings between Start and Stop.
func runMock(mon *monitor.Monitor) {
	for {
		mon.WaitForStart()
		stop := mon.StopChan()
		mon.SetConnected(true)
		const ambient = 110.0
		tip := 6.0
		running := true
		for running {
			select {
			case <-stop:
				running = false
			default:
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
		mon.SetConnected(false)
	}
}

func waitForSignal() {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
}
