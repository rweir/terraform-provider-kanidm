//go:build acc

package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Round-trip `allow_insecure_client_disable_pkce` through create /
// no-op / toggle off / toggle on. The matching Kanidm CLI commands are:
//
//	kanidm system oauth2 warning-insecure-client-disable-pkce <rs>   (true)
//	kanidm system oauth2 warning-enable-pkce                  <rs>   (false)
//
// Server-side attribute: `oauth2_allow_insecure_client_disable_pkce`.
// This relaxes Kanidm's PKCE requirement for one specific client —
// only useful for confidential clients (like older Forgejo, Netbox,
// OpenGist) that don't send `code_challenge`.
func TestAccOAuth2Basic_disablePKCE(t *testing.T) {
	groupName := uniq("group")
	clientName := uniq("client")

	cfg := func(disable bool) string {
		return fmt.Sprintf(`
resource "kanidm_group" "users" {
  id = %q
}

resource "kanidm_oauth2_basic" "test" {
  name                               = %q
  displayname                        = "TF Acceptance Test"
  origin                             = "https://example.test"
  redirect_uris                      = ["https://example.test/oauth2/callback"]
  allow_insecure_client_disable_pkce = %t

  scope_map {
    group  = kanidm_group.users.id
    scopes = ["openid", "email", "profile"]
  }
}
`, groupName, clientName, disable)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg(true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "allow_insecure_client_disable_pkce", "true"),
				),
			},
			{
				Config:   cfg(true),
				PlanOnly: true,
			},
			{
				Config: cfg(false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "allow_insecure_client_disable_pkce", "false"),
				),
			},
			{
				Config:   cfg(false),
				PlanOnly: true,
			},
			{
				Config: cfg(true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "allow_insecure_client_disable_pkce", "true"),
				),
			},
		},
	})
}
