package main

import (
	"flag"
	"fmt"
	"runtime"
	"runtime/debug"
)

// Version is set at build time with -ldflags "-X main.Version=v0.2.0"; without
// it, the module's own build info is used, which is what `go install` gives.
var Version = ""

func versionString() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok {
		for _, s := range bi.Settings {
			if s.Key == "vcs.revision" && len(s.Value) >= 7 {
				return "dev-" + s.Value[:7]
			}
		}
	}
	return "dev"
}

func cmdVersion(args []string) error {
	fs := flag.NewFlagSet("version", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable output")
	fs.Parse(args)
	if *asJSON {
		return printJSON(map[string]string{
			"version": versionString(),
			"go":      runtime.Version(),
			"os":      runtime.GOOS,
			"arch":    runtime.GOARCH,
		})
	}
	fmt.Printf("mesh %s (%s %s/%s)\n", versionString(), runtime.Version(), runtime.GOOS, runtime.GOARCH)
	return nil
}
