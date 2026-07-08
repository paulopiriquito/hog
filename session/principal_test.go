package session

import (
	"context"
	"testing"
)

func TestPrincipalDerivationExcludesFingerprint(t *testing.T) {
	s := &Session{Subject: "u-1", Passport: map[string]any{"email": "a@b.co"},
		Groups: []string{"g1"}, AccessToken: "at", Fingerprint: "fp"}
	p := s.Principal()
	if p.Subject != "u-1" || p.AccessToken != "at" || p.Passport["email"] != "a@b.co" || len(p.Groups) != 1 {
		t.Fatalf("principal = %+v", p)
	}
}

func TestContextRoundTripAndAccessors(t *testing.T) {
	bare := context.Background()
	if _, ok := FromContext(bare); ok {
		t.Fatal("bare context must have no principal")
	}
	if InGroup(bare, "g1") {
		t.Fatal("InGroup on bare ctx must be false")
	}
	if _, ok := Claim(bare, "email"); ok {
		t.Fatal("Claim on bare ctx must be false")
	}

	p := &Principal{Subject: "u-1", Passport: map[string]any{"email": "a@b.co"}, Groups: []string{"g1", "g2"}, AccessToken: "at"}
	ctx := WithPrincipal(bare, p)
	got, ok := FromContext(ctx)
	if !ok || got.Subject != "u-1" {
		t.Fatalf("FromContext = %+v %v", got, ok)
	}
	if !InGroup(ctx, "g2") || InGroup(ctx, "nope") {
		t.Fatal("InGroup wrong")
	}
	if v, ok := Claim(ctx, "email"); !ok || v != "a@b.co" {
		t.Fatalf("Claim = %v %v", v, ok)
	}
	if _, ok := Claim(ctx, "absent"); ok {
		t.Fatal("absent claim must be false")
	}
}
