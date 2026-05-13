package forward_test

import (
	"strings"
	"testing"

	"github.com/paulopiriquito/hog/pkg/forward"
)

func TestConfigValidate_AcceptsMinimalValid(t *testing.T) {
	cfg := forward.Config{
		Headers: []forward.Header{
			{Claim: "sub", Name: "X-User-Id"},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestConfigValidate_AcceptsMappingRules(t *testing.T) {
	cfg := forward.Config{
		Headers: []forward.Header{
			{
				Claim:  "memberof",
				Name:   "X-User-Roles",
				Mapping: []forward.Rule{
					{From: "cn=KRONOS,", To: "KRONOS-USER"},
				},
			},
		},
	}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}
}

func TestConfigValidate_RejectsEmptyHeaders(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "headers list is empty") {
		t.Fatalf("expected empty-headers error, got: %v", err)
	}
}

func TestConfigValidate_RejectsMissingClaim(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{{Name: "X-Foo"}}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "claim is required") {
		t.Fatalf("expected missing-claim error, got: %v", err)
	}
}

func TestConfigValidate_RejectsMissingHeader(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{{Claim: "sub"}}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "header is required") {
		t.Fatalf("expected missing-header error, got: %v", err)
	}
}

func TestConfigValidate_RejectsDuplicateHeaderName(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "sub", Name: "X-User-Id"},
		{Claim: "uid", Name: "X-User-Id"},
	}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "duplicate header") {
		t.Fatalf("expected duplicate-header error, got: %v", err)
	}
}

func TestConfigValidate_RejectsEmptyMappingArray(t *testing.T) {
	cfg := forward.Config{Headers: []forward.Header{
		{Claim: "memberof", Name: "X-Roles", Mapping: []forward.Rule{}},
	}}
	err := cfg.Validate()
	if err == nil || !strings.Contains(err.Error(), "mapping is present but empty") {
		t.Fatalf("expected empty-mapping error, got: %v", err)
	}
}

func TestConfigValidate_RejectsEmptyFromOrTo(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule forward.Rule
		want string
	}{
		{"empty from", forward.Rule{From: "", To: "X"}, "from is required"},
		{"empty to", forward.Rule{From: "X", To: ""}, "to is required"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := forward.Config{Headers: []forward.Header{
				{Claim: "memberof", Name: "X-Roles", Mapping: []forward.Rule{tc.rule}},
			}}
			err := cfg.Validate()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q in error, got: %v", tc.want, err)
			}
		})
	}
}
