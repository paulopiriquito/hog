package idp

import (
	"context"
	"testing"

	"github.com/coreos/go-oidc/v3/oidc"
)

// Proves the fake IdP harness serves valid discovery + JWKS that go-oidc accepts.
func TestFakeIdPDiscovery(t *testing.T) {
	f := newFakeIdP(t, "client-1")
	provider, err := oidc.NewProvider(context.Background(), f.srv.URL)
	if err != nil {
		t.Fatalf("discovery against fake IdP failed: %v", err)
	}
	if provider.Endpoint().TokenURL != f.srv.URL+"/token" {
		t.Fatalf("token endpoint = %q", provider.Endpoint().TokenURL)
	}
}
