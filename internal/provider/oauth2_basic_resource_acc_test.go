//go:build acc

package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Round-trip the core fields: name, displayname, origin (landing URL),
// redirect_uris (multi-valued, set semantics), and a single scope_map
// keyed off a freshly-created group.
//
// Regression coverage for two bugs this provider used to have:
//
//   - origin / redirect_uris were inverted in Read and Update, so the
//     post-apply state never matched the plan.
//   - redirect_uris was a List, so Kanidm returning the multi-valued
//     attribute in a different order than we PATCHed caused
//     "provider produced inconsistent result after apply".
func TestAccOAuth2Basic_basic(t *testing.T) {
	groupName := uniq("group")
	clientName := uniq("client")
	cb1 := "https://example.test/oauth2/callback"
	cb2 := "https://example.test/oauth2/other"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "kanidm_group" "users" {
  id = %q
}

resource "kanidm_oauth2_basic" "test" {
  name        = %q
  displayname = "TF Acceptance Test"
  origin      = "https://example.test"
  redirect_uris = [%q, %q]

  scope_map {
    group  = kanidm_group.users.id
    scopes = ["openid", "email", "profile"]
  }
}
`, groupName, clientName, cb1, cb2),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "name", clientName),
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "displayname", "TF Acceptance Test"),
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "origin", "https://example.test"),
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "redirect_uris.#", "2"),
					resource.TestCheckTypeSetElemAttr("kanidm_oauth2_basic.test", "redirect_uris.*", cb1),
					resource.TestCheckTypeSetElemAttr("kanidm_oauth2_basic.test", "redirect_uris.*", cb2),
					resource.TestCheckResourceAttrSet("kanidm_oauth2_basic.test", "client_secret"),
				),
			},
			// Re-apply identical config — should be a clean no-op
			// plan. This is the bit that catches read-time field
			// inversion and list/set instability.
			{
				Config: fmt.Sprintf(`
resource "kanidm_group" "users" {
  id = %q
}

resource "kanidm_oauth2_basic" "test" {
  name        = %q
  displayname = "TF Acceptance Test"
  origin      = "https://example.test"
  redirect_uris = [%q, %q]

  scope_map {
    group  = kanidm_group.users.id
    scopes = ["openid", "email", "profile"]
  }
}
`, groupName, clientName, cb1, cb2),
				PlanOnly: true,
			},
			// Reorder redirect_uris in the config — must still be a
			// no-op because the field is a set.
			{
				Config: fmt.Sprintf(`
resource "kanidm_group" "users" {
  id = %q
}

resource "kanidm_oauth2_basic" "test" {
  name        = %q
  displayname = "TF Acceptance Test"
  origin      = "https://example.test"
  redirect_uris = [%q, %q]

  scope_map {
    group  = kanidm_group.users.id
    scopes = ["openid", "email", "profile"]
  }
}
`, groupName, clientName, cb2, cb1),
				PlanOnly: true,
			},
			// Mutate displayname — should produce a single in-place
			// update.
			{
				Config: fmt.Sprintf(`
resource "kanidm_group" "users" {
  id = %q
}

resource "kanidm_oauth2_basic" "test" {
  name        = %q
  displayname = "Renamed"
  origin      = "https://example.test"
  redirect_uris = [%q, %q]

  scope_map {
    group  = kanidm_group.users.id
    scopes = ["openid", "email", "profile"]
  }
}
`, groupName, clientName, cb1, cb2),
				Check: resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "displayname", "Renamed"),
			},
		},
	})
}

// Variant with no redirect_uris (the Grafana-style "landing URL only"
// shape — origin is set, redirect_uris is null).
func TestAccOAuth2Basic_noRedirectURIs(t *testing.T) {
	groupName := uniq("group")
	clientName := uniq("client")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "kanidm_group" "users" {
  id = %q
}

resource "kanidm_oauth2_basic" "test" {
  name        = %q
  displayname = "TF Acceptance Test (landing only)"
  origin      = "https://example.test/login/generic_oauth"

  scope_map {
    group  = kanidm_group.users.id
    scopes = ["openid", "email", "profile", "groups"]
  }
}
`, groupName, clientName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_oauth2_basic.test", "origin", "https://example.test/login/generic_oauth"),
					resource.TestCheckResourceAttrSet("kanidm_oauth2_basic.test", "client_secret"),
				),
			},
			{
				Config: fmt.Sprintf(`
resource "kanidm_group" "users" {
  id = %q
}

resource "kanidm_oauth2_basic" "test" {
  name        = %q
  displayname = "TF Acceptance Test (landing only)"
  origin      = "https://example.test/login/generic_oauth"

  scope_map {
    group  = kanidm_group.users.id
    scopes = ["openid", "email", "profile", "groups"]
  }
}
`, groupName, clientName),
				PlanOnly: true,
			},
		},
	})
}
