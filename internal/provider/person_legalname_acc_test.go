//go:build acc

package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Round-trip legalname: declare a value, refresh as a no-op, mutate
// the value, then drop it from config and confirm refresh doesn't try
// to revert (the Optional+Computed+UseStateForUnknown contract — when
// config stops declaring an attr, the provider leaves whatever the
// server currently has alone).
func TestAccPerson_legalname(t *testing.T) {
	id := uniq("person-legal")

	withLegalname := func(legal string) string {
		return fmt.Sprintf(`
resource "kanidm_person" "test" {
  id          = %q
  displayname = "TF Legal Test"
  legalname   = %q
}
`, id, legal)
	}
	withoutLegalname := fmt.Sprintf(`
resource "kanidm_person" "test" {
  id          = %q
  displayname = "TF Legal Test"
}
`, id)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create with legalname declared.
			{
				Config: withLegalname("Roberto Initial"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_person.test", "legalname", "Roberto Initial"),
				),
			},
			{
				Config:   withLegalname("Roberto Initial"),
				PlanOnly: true,
			},
			// Mutate.
			{
				Config: withLegalname("Roberto Renamed"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_person.test", "legalname", "Roberto Renamed"),
				),
			},
			{
				Config:   withLegalname("Roberto Renamed"),
				PlanOnly: true,
			},
			// Drop legalname from config entirely. The server still
			// has "Roberto Renamed"; tofu must leave that alone (not
			// PATCH it away).
			{
				Config:   withoutLegalname,
				PlanOnly: true,
			},
			// Import round-trip with no legalname declared in config.
			{
				ResourceName:                         "kanidm_person.test",
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateId:                        id,
				ImportStateVerifyIdentifierAttribute: "id",
				ImportStateVerifyIgnore: []string{
					"password", "credential_reset_token",
					"credential_reset_token_ttl", "generate_credential_reset_token",
				},
			},
		},
	})
}
