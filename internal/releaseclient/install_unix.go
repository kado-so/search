//go:build !windows

package releaseclient

func (manager Manager) installCandidate(
	candidate, target, expected string,
) (bool, error) {
	return false, manager.replaceExpected(candidate, target, expected)
}
