//go:build acc

package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Round-trip `jwt_legacy_crypto_enable` through create / no-op /
// toggle off / toggle on. The matching Kanidm CLI commands are:
//
//	kanidm system oauth2 jwt-legacy-crypto-enable  <rs>   (true)
//	kanidm system oauth2 jwt-legacy-crypto-disable <rs>   (false)
//
// Server-side attribute: `oauth2_jwt_legacy_crypto_enable`. Enables
// RS256 JWT signing alongside Kanidm's default ES256 — needed for
// older clients (e.g. Netbox's python-social-auth) that can't speak
// ES256.
func TestAccOAuth2Basic_jwtLegacyCrypto(t *testing.T) {
	groupName := uniq("group")
	clientName := uniq("client")

	cfg := func(enable bool) string {
		return fmt.Sprintf(`
resource "kanidm_group" "users" {
  id = %q
}

resource "kanidm_oauth2_basic" "test" {
  name                     = %q
  displayname              = "TF Acceptance Test"
  origin                   = "https://example.test"
  redirect_uris            = ["https://example.test/oauth2/callback"]
  jwt_legacy_crypto_enable = %t

  scope_map {
    group  = kanidm_group.users.id
    scopes = ["openid", "email", "profile"]
  }
}
`, groupName, clientName, enable)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg(true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "jwt_legacy_crypto_enable", "true"),
				),
			},
			{
				Config:   cfg(true),
				PlanOnly: true,
			},
			{
				Config: cfg(false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "jwt_legacy_crypto_enable", "false"),
				),
			},
			{
				Config:   cfg(false),
				PlanOnly: true,
			},
			{
				Config: cfg(true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "jwt_legacy_crypto_enable", "true"),
				),
			},
		},
	})
}
