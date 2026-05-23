//go:build acc

package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Basic person lifecycle: create with displayname + mail, refresh
// no-op, import round-trip, mutate displayname, mutate mail.
//
// `mail` is multi-valued on the Kanidm side; if Kanidm stores it
// unordered, this test will surface the same List-vs-Set issue we
// fixed for redirect_uris.
func TestAccPerson_basic(t *testing.T) {
	id := uniq("person")

	cfg := func(display, m1, m2 string) string {
		return fmt.Sprintf(`
resource "kanidm_person" "test" {
  id          = %q
  displayname = %q
  mail        = [%q, %q]
}
`, id, display, m1, m2)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg("TF Acceptance Test", "tfa@example.test", "tfa-alt@example.test"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_person.test", "id", id),
					resource.TestCheckResourceAttr("kanidm_person.test", "displayname", "TF Acceptance Test"),
					resource.TestCheckResourceAttr("kanidm_person.test", "mail.#", "2"),
				),
			},
			{
				Config:   cfg("TF Acceptance Test", "tfa@example.test", "tfa-alt@example.test"),
				PlanOnly: true,
			},
			{
				ResourceName:                         "kanidm_person.test",
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateId:                        id,
				ImportStateVerifyIdentifierAttribute: "id",
				// password is write-only; the credential_reset_token
				// fields are session-scoped and not idempotent.
				ImportStateVerifyIgnore: []string{
					"password",
					"credential_reset_token",
					"credential_reset_token_ttl",
					"generate_credential_reset_token",
				},
			},
			// Mutate displayname.
			{
				Config: cfg("Renamed", "tfa@example.test", "tfa-alt@example.test"),
				Check: resource.TestCheckResourceAttr("kanidm_person.test", "displayname", "Renamed"),
			},
			{
				Config:   cfg("Renamed", "tfa@example.test", "tfa-alt@example.test"),
				PlanOnly: true,
			},
			// Mutate mail — drop one, add another. Order changed too,
			// which catches order-sensitive Read parsers.
			{
				Config: cfg("Renamed", "tfa-new@example.test", "tfa@example.test"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_person.test", "mail.#", "2"),
				),
			},
			{
				Config:   cfg("Renamed", "tfa-new@example.test", "tfa@example.test"),
				PlanOnly: true,
			},
		},
	})
}

// Minimal person — only id + displayname, no mail. Catches
// null-vs-empty bugs on the mail attribute.
func TestAccPerson_minimal(t *testing.T) {
	id := uniq("person-min")

	cfg := fmt.Sprintf(`
resource "kanidm_person" "test" {
  id          = %q
  displayname = "Minimal"
}
`, id)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_person.test", "id", id),
					resource.TestCheckResourceAttr("kanidm_person.test", "displayname", "Minimal"),
				),
			},
			{
				Config:   cfg,
				PlanOnly: true,
			},
		},
	})
}
