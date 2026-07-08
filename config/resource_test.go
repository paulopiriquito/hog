package config

import "testing"

func TestDecodeAll(t *testing.T) {
	data := []byte(`
apiVersion: hog.dev/v1
kind: Route
metadata:
  name: spa
  labels: { app: dash, tier: web }
spec:
  match: /app/
---
apiVersion: hog.dev/v1
kind: Gateway
metadata: { name: hog }
spec:
  listen: ":8080"
`)
	rs, err := DecodeAll(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rs) != 2 {
		t.Fatalf("got %d resources, want 2", len(rs))
	}
	if rs[0].Kind != "Route" || rs[0].Metadata.Name != "spa" {
		t.Fatalf("resource[0] = %+v", rs[0])
	}
	if rs[0].Metadata.Labels["tier"] != "web" {
		t.Fatalf("labels = %v", rs[0].Metadata.Labels)
	}
	if rs[1].Kind != "Gateway" {
		t.Fatalf("resource[1].Kind = %q", rs[1].Kind)
	}
}

func TestDecodeAllSkipsEmptyDocs(t *testing.T) {
	rs, err := DecodeAll([]byte("---\n\n---\nkind: Gateway\nmetadata: {name: hog}\n"))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rs) != 1 {
		t.Fatalf("got %d resources, want 1", len(rs))
	}
}
