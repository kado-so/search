package agentauth

import (
	"context"
	"crypto/rand"
	"errors"
	"net/url"
	"testing"
)

func TestLinkAccountUsesDiscoveredDeviceStyleEndpoints(t *testing.T) {
	t.Parallel()
	fake := newFakeAuthServer(newFakePersistentState())
	defer fake.close()
	base, err := url.Parse(fake.issuer())
	if err != nil {
		t.Fatal(err)
	}
	client, err := NewClient(base, fake.server.Client(), DefaultLimits(), rand.Reader)
	if err != nil {
		t.Fatalf("NewClient() error = %v", err)
	}
	var shown LinkAuthorization
	status, err := client.LinkAccount(
		context.Background(),
		SessionToken{accessToken: "test-link-token"},
		func(value LinkAuthorization) error {
			shown = value
			return nil
		},
	)
	if err != nil {
		t.Fatalf("LinkAccount() error = %v", err)
	}
	if status.Status != LinkStatusLinked || shown.UserCode != "ABCD-2345" ||
		shown.VerificationURI != fake.issuer()+"/link" || shown.DeviceCode == "" {
		t.Fatalf("LinkAccount() status=%#v authorization=%#v", status, shown)
	}
}

func TestLinkAccountsAttachesEveryAdditionalIdentityBeforeOneApproval(t *testing.T) {
	t.Parallel()
	fake := newFakeAuthServer(newFakePersistentState())
	defer fake.close()
	base, _ := url.Parse(fake.issuer())
	client, err := NewClient(base, fake.server.Client(), DefaultLimits(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	notifications := 0
	status, err := client.LinkAccounts(
		context.Background(),
		[]SessionToken{
			{accessToken: "test-link-token"},
			{accessToken: "second-link-token"},
			{accessToken: "third-link-token"},
		},
		func(LinkAuthorization) error {
			notifications++
			return nil
		},
	)
	if err != nil || status.Status != LinkStatusLinked {
		t.Fatalf("LinkAccounts() status=%#v error=%v", status, err)
	}
	fake.mu.Lock()
	attached := append([]string(nil), fake.linkAttachedTokens...)
	fake.mu.Unlock()
	if notifications != 1 || len(attached) != 2 ||
		attached[0] != "Bearer second-link-token" ||
		attached[1] != "Bearer third-link-token" {
		t.Fatalf("notifications=%d attached=%q", notifications, attached)
	}
}

func TestLinkAccountClassifiesDeniedAndExpiredPolling(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		wire string
		want error
	}{
		{name: "denied", wire: "access_denied", want: ErrLinkDenied},
		{name: "expired", wire: "expired_token", want: ErrLinkExpired},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			fake := newFakeAuthServer(newFakePersistentState())
			defer fake.close()
			fake.linkPollError = test.wire
			base, _ := url.Parse(fake.issuer())
			client, err := NewClient(base, fake.server.Client(), DefaultLimits(), rand.Reader)
			if err != nil {
				t.Fatal(err)
			}
			_, err = client.LinkAccount(
				context.Background(),
				SessionToken{accessToken: "test-link-token"},
				func(LinkAuthorization) error { return nil },
			)
			if !errors.Is(err, test.want) {
				t.Fatalf("LinkAccount() error = %v, want %v", err, test.want)
			}
		})
	}
}
