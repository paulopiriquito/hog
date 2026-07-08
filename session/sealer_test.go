package session

import "testing"

func TestExportedSealerRoundTrip(t *testing.T) {
	s, err := NewSealer([]byte(key32))
	if err != nil {
		t.Fatal(err)
	}
	ct, err := s.Seal([]byte("transient"), nil)
	if err != nil {
		t.Fatal(err)
	}
	pt, err := s.Open(ct, nil)
	if err != nil || string(pt) != "transient" {
		t.Fatalf("round-trip: %q %v", pt, err)
	}
	if _, err := NewSealer([]byte("short")); err == nil {
		t.Fatal("want error for non-32-byte key")
	}
}
