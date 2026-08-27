// Package installchannel defines the closed set of Kado distribution owners.
package installchannel

const (
	Direct    = "direct"
	Homebrew  = "homebrew"
	WinGet    = "winget"
	Scoop     = "scoop"
	Deb       = "deb"
	RPM       = "rpm"
	Container = "container"
)

// Valid reports whether value can be stamped into a release build.
func Valid(value string) bool {
	switch value {
	case Direct, Homebrew, WinGet, Scoop, Deb, RPM, Container:
		return true
	default:
		return false
	}
}
