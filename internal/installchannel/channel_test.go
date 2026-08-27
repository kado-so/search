package installchannel

import "testing"

func TestValidIsClosed(t *testing.T) {
	t.Parallel()

	for _, value := range []string{Direct, Homebrew, WinGet, Scoop, Deb, RPM, Container} {
		if !Valid(value) {
			t.Fatalf("Valid(%q) = false", value)
		}
	}
	for _, value := range []string{"", "unknown", "brew", "apt", "DIRECT", "direct\ncontainer"} {
		if Valid(value) {
			t.Fatalf("Valid(%q) = true", value)
		}
	}
}
