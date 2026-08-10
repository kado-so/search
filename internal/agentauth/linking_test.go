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
