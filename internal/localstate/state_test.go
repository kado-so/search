package localstate

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestHostIsStableAndIdentitiesAreSorted(t *testing.T) {
	root := filepath.Join(t.TempDir(), "kado")
	first, err := EnsureHost(root)
	if err != nil {
		t.Fatal(err)
	}
	second, err := EnsureHost(root)
	if err != nil {
		t.Fatal(err)
	}
	if first != second || first.ID == "" {
		t.Fatalf("hosts = %#v and %#v", first, second)
	}
	for _, agent := range []string{"codex", "claude-code", "codex"} {
		if err := AddIdentity(root, agent); err != nil {
			t.Fatal(err)
		}
	}
	got, err := ListIdentities(root)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"claude-code", "codex"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("identities = %v, want %v", got, want)
	}
	if err := RemoveIdentity(root, "codex"); err != nil {
		t.Fatal(err)
	}
	got, err = ListIdentities(root)
	if err != nil || !reflect.DeepEqual(got, []string{"claude-code"}) {
		t.Fatalf("identities after remove = %v, %v", got, err)
	}
}
