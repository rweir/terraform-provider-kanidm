//go:build acc

package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Round-trip `prefer_short_username` through create / no-op / toggle off /
// toggle on. The matching Kanidm CLI commands are:
//
//	kanidm system oauth2 prefer-short-username <rs>   (set true)
//	kanidm system oauth2 prefer-spn-username   <rs>   (set false)
//
// The server-side attribute is `oauth2_prefer_short_username`. Setting
// it changes the OIDC `preferred_username` claim from the full SPN
// (`name@domain`) to the bare `name` — matters for clients that treat
// `preferred_username` as a plain username.
func TestAccOAuth2Basic_preferShortUsername(t *testing.T) {
	groupName := uniq("group")
	clientName := uniq("client")

	cfg := func(preferShort bool) string {
		return fmt.Sprintf(`
resource "kanidm_group" "users" {
  id = %q
}

resource "kanidm_oauth2_basic" "test" {
  name                  = %q
  displayname           = "TF Acceptance Test"
  origin                = "https://example.test"
  redirect_uris         = ["https://example.test/oauth2/callback"]
  prefer_short_username = %t

  scope_map {
    group  = kanidm_group.users.id
    scopes = ["openid", "email", "profile"]
  }
}
`, groupName, clientName, preferShort)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create with prefer_short_username = true.
			{
				Config: cfg(true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "prefer_short_username", "true"),
				),
			},
			// Refresh — must not drift.
			{
				Config:   cfg(true),
				PlanOnly: true,
			},
			// Toggle off.
			{
				Config: cfg(false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "prefer_short_username", "false"),
				),
			},
			// Refresh with toggle-off — must not drift either.
			{
				Config:   cfg(false),
				PlanOnly: true,
			},
			// Toggle back on.
			{
				Config: cfg(true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "prefer_short_username", "true"),
				),
			},
		},
	})
}
