package config

import "testing"

func TestExpandEnv(t *testing.T) {
	lookup := func(k string) (string, bool) {
		m := map[string]string{"HOST": "example.com", "EMPTY": ""}
		v, ok := m[k]
		return v, ok
	}
	cases := []struct {
		name, in, want string
		wantErr        bool
	}{
		{name: "plain", in: "no vars", want: "no vars"},
		{name: "simple", in: "https://${HOST}/x", want: "https://example.com/x"},
		{name: "default used", in: "${MISSING:-fallback}", want: "fallback"},
		{name: "default ignored", in: "${HOST:-fallback}", want: "example.com"},
		{name: "empty is set", in: "${EMPTY:-fallback}", want: ""},
		{name: "missing required", in: "${MISSING}", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExpandEnv(tc.in, lookup)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q want %q", got, tc.want)
			}
		})
	}
}
