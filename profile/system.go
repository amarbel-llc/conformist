package profile

import (
	"regexp"
	"runtime"
)

// systemRegex matches a Nix-style system string (`x86_64-linux`,
// `aarch64-darwin`), the spelling a profile uses to key per-system artifacts.
var systemRegex = regexp.MustCompile("^[a-z0-9_]+-[a-z]+$")

// nixArch maps Go's GOARCH to the architecture half of a Nix system string.
var nixArch = map[string]string{
	"amd64": "x86_64",
	"arm64": "aarch64",
	"386":   "i686",
	"arm":   "armv7l",
}

// HostSystem returns the Nix-style system string of the running binary, e.g.
// `x86_64-linux` or `aarch64-darwin`. An architecture Nix names differently
// than Go and that is not mapped falls back to Go's own GOARCH, which then
// simply matches no profile entry.
func HostSystem() string {
	arch, ok := nixArch[runtime.GOARCH]
	if !ok {
		arch = runtime.GOARCH
	}

	return arch + "-" + runtime.GOOS
}
