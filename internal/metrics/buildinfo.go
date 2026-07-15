package metrics

import (
	"runtime"
	"runtime/debug"
	"sync"
)

// buildVersion can be overridden at link time for release builds:
//
//	go build -ldflags "-X github.com/awlx/meater-golang/internal/metrics.buildVersion=v1.2.3"
//
// Left empty it falls back to whatever the module system recorded, which for a
// `go install`ed binary is the module version and for a VCS build is the commit.
var buildVersion string

var versionOnce = sync.OnceValue(func() string {
	if buildVersion != "" {
		return buildVersion
	}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	// A binary built from a checkout has no module version ("(devel)"), but the
	// Go toolchain stamps the commit it was built from; prefer that.
	for _, s := range info.Settings {
		if s.Key == "vcs.revision" && s.Value != "" {
			return s.Value
		}
	}
	if info.Main.Version != "" {
		return info.Main.Version
	}
	return "unknown"
})

// version reports the running binary's version for meater_build_info.
func version() string { return versionOnce() }

// goVersion reports the Go runtime version for meater_build_info.
func goVersion() string { return runtime.Version() }
