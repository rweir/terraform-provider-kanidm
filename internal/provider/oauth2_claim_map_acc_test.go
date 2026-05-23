//go:build acc

package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Exercise `claim_map` through create-with-one-entry / no-op /
// add-a-second / mutate-the-values / remove-one. The matching Kanidm
// CLI commands are:
//
//	kanidm system oauth2 update-claim-map <rs> <name> <group> <v1> [<v2>...]
//	kanidm system oauth2 delete-claim-map <rs> <name> <group>
//
// Server-side, each (name, group) pair is its own sub-resource at
// /v1/oauth2/<rs>/_claimmap/<name>/<group>; the values list lives in
// the request body. The provider exposes this as a `claim_map` set
// nested block on `kanidm_oauth2_basic`:
//
//	claim_map {
//	  name   = "grafana_role"
//	  group  = kanidm_group.admins.id
//	  values = ["Admin"]
//	}
//
// This is the most complex of the four out-of-band attrs because it
// has its own sub-resource lifecycle (create / update / delete each
// (name, group) tuple independently) and a non-trivial Read parse.
func TestAccOAuth2Basic_claimMap(t *testing.T) {
	clientName := uniq("client")
	adminsGroup := uniq("group-admins")
	editorsGroup := uniq("group-editors")
	usersGroup := uniq("group-users")

	// Common preamble: three groups + an oauth2 client with one
	// claim_map (the admins one). We mutate just the oauth2 resource
	// across steps.
	groupsHCL := fmt.Sprintf(`
resource "kanidm_group" "admins"  { id = %q }
resource "kanidm_group" "editors" { id = %q }
resource "kanidm_group" "users"   { id = %q }
`, adminsGroup, editorsGroup, usersGroup)

	scopeMap := `
  scope_map {
    group  = kanidm_group.users.id
    scopes = ["openid", "email", "profile", "groups"]
  }
`

	clientHCL := func(claims string) string {
		return fmt.Sprintf(`
resource "kanidm_oauth2_basic" "test" {
  name          = %q
  displayname   = "TF Acceptance Test"
  origin        = "https://example.test"
  redirect_uris = ["https://example.test/oauth2/callback"]
%s
%s
}
`, clientName, scopeMap, claims)
	}

	oneAdmin := `
  claim_map {
    name   = "grafana_role"
    group  = kanidm_group.admins.id
    values = ["Admin"]
  }
`

	adminAndEditor := `
  claim_map {
    name   = "grafana_role"
    group  = kanidm_group.admins.id
    values = ["Admin"]
  }
  claim_map {
    name   = "grafana_role"
    group  = kanidm_group.editors.id
    values = ["Editor"]
  }
`

	adminMultiValue := `
  claim_map {
    name   = "grafana_role"
    group  = kanidm_group.admins.id
    values = ["Admin", "GrafanaAdmin"]
  }
`

	noClaimMaps := ``

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create with a single claim_map entry.
			{
				Config: groupsHCL + clientHCL(oneAdmin),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "claim_map.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("kanidm_oauth2_basic.test", "claim_map.*", map[string]string{
						"name":     "grafana_role",
						"group":    adminsGroup,
						"values.#": "1",
					}),
				),
			},
			// Refresh — no drift.
			{
				Config:   groupsHCL + clientHCL(oneAdmin),
				PlanOnly: true,
			},
			// Add a second claim_map entry (Editor).
			{
				Config: groupsHCL + clientHCL(adminAndEditor),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "claim_map.#", "2"),
				),
			},
			{
				Config:   groupsHCL + clientHCL(adminAndEditor),
				PlanOnly: true,
			},
			// Mutate the values of an existing entry (Admin -> Admin + GrafanaAdmin).
			// Removes the Editor entry in the same step.
			{
				Config: groupsHCL + clientHCL(adminMultiValue),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "claim_map.#", "1"),
					resource.TestCheckTypeSetElemNestedAttrs("kanidm_oauth2_basic.test", "claim_map.*", map[string]string{
						"name":     "grafana_role",
						"group":    adminsGroup,
						"values.#": "2",
					}),
				),
			},
			{
				Config:   groupsHCL + clientHCL(adminMultiValue),
				PlanOnly: true,
			},
			// Drop all claim_maps. After this the server should have
			// none for this client; refresh must not show drift.
			{
				Config: groupsHCL + clientHCL(noClaimMaps),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "claim_map.#", "0"),
				),
			},
			{
				Config:   groupsHCL + clientHCL(noClaimMaps),
				PlanOnly: true,
			},
		},
	})
}
