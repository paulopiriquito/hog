package session

import "testing"

func TestProjectPassportPrefersUserinfoOverIDToken(t *testing.T) {
	idClaims := map[string]any{"email": "id@x.co", "name": "ID Name"}
	userinfo := map[string]any{"email": "ui@x.co", "departmentnumber": "42"}
	got := projectPassport([]string{"email", "name", "departmentnumber", "absent"}, idClaims, userinfo)
	if got["email"] != "ui@x.co" {
		t.Errorf("email = %v", got["email"])
	}
	if got["name"] != "ID Name" {
		t.Errorf("name = %v", got["name"])
	}
	if got["departmentnumber"] != "42" {
		t.Errorf("dept = %v", got["departmentnumber"])
	}
	if _, ok := got["absent"]; ok {
		t.Errorf("absent claim should be skipped")
	}
}

func TestProjectGroupsFilterAndRender(t *testing.T) {
	userinfo := map[string]any{"isMemberOf": []any{
		"cn=PT-LM-ROLE-invoice-portal-invoice_download,ou=invoice-portal,ou=applicationRole,ou=role,ou=PT-LM,o=corp.leroymerlin.com",
		"cn=PT-LM-ROLE-invoice-portal-search_invoices,ou=invoice-portal,ou=applicationRole,ou=role,ou=PT-LM,o=corp.leroymerlin.com",
		"cn=LMPT-TODOS,ou=mailGroup,ou=MirapointMessagingSystem,ou=PT-LM,o=corp.leroymerlin.com",
		"cn=GLOBAL-ROLE-GITHUB-ACCESS,ou=GITHUB,ou=applicationRole,ou=role,ou=GLOBAL,O=CORP.LEROYMERLIN.COM",
	}}

	g := projectGroups(&GroupsConfig{
		Source: "isMemberOf", Match: []string{"ou=invoice-portal,ou=applicationRole"}, Render: "cn", As: "groups",
	}, userinfo)
	want := []string{"PT-LM-ROLE-invoice-portal-invoice_download", "PT-LM-ROLE-invoice-portal-search_invoices"}
	if len(g) != 2 || g[0] != want[0] || g[1] != want[1] {
		t.Fatalf("cn render = %v, want %v", g, want)
	}

	g2 := projectGroups(&GroupsConfig{
		Source: "isMemberOf", Match: []string{"OU=APPLICATIONROLE"}, Render: "dn", As: "groups",
	}, userinfo)
	if len(g2) != 3 {
		t.Fatalf("dn render count = %d, want 3 (%v)", len(g2), g2)
	}
	if g2[0] != userinfo["isMemberOf"].([]any)[0].(string) {
		t.Fatalf("dn render should keep whole DN, got %q", g2[0])
	}
}

func TestProjectGroupsEmptyWhenSourceMissingOrNoMatch(t *testing.T) {
	if g := projectGroups(&GroupsConfig{Source: "isMemberOf", Match: []string{"ou=x"}, Render: "cn"}, map[string]any{}); len(g) != 0 {
		t.Fatalf("missing source ⇒ empty, got %v", g)
	}
	ui := map[string]any{"isMemberOf": []any{"cn=foo,ou=bar"}}
	if g := projectGroups(&GroupsConfig{Source: "isMemberOf", Match: []string{"ou=nope"}, Render: "cn"}, ui); len(g) != 0 {
		t.Fatalf("no match ⇒ empty, got %v", g)
	}
}
