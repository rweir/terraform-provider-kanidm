//go:build acc

package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Basic lifecycle for kanidm_application — create, refresh, import,
// mutate displayname. linked_group is a kanidm group; we create one
// alongside.
//
// Application service accounts are managed via the SCIM v1 API
// (/scim/v1/Application), not the legacy /v1 API the other resources
// use. The provider exposes them as kanidm_application — distinct
// from kanidm_service_account because the create endpoint, JSON
// schema, and resulting class set all differ.
func TestAccApplication_basic(t *testing.T) {
	linked := uniq("group-linked")
	app := uniq("app")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "kanidm_group" "linked" {
  id = %q
}

resource "kanidm_application" "test" {
  name         = %q
  displayname  = "TF Acceptance Test App"
  linked_group = kanidm_group.linked.id
}
`, linked, app),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_application.test", "name", app),
					resource.TestCheckResourceAttr("kanidm_application.test", "displayname", "TF Acceptance Test App"),
					resource.TestCheckResourceAttr("kanidm_application.test", "linked_group", linked),
					// uuid is what app_password resources will reference.
					resource.TestMatchResourceAttr("kanidm_application.test", "uuid",
						regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)),
				),
			},
			{
				Config: fmt.Sprintf(`
resource "kanidm_group" "linked" {
  id = %q
}

resource "kanidm_application" "test" {
  name         = %q
  displayname  = "TF Acceptance Test App"
  linked_group = kanidm_group.linked.id
}
`, linked, app),
				PlanOnly: true,
			},
			{
				ResourceName:                         "kanidm_application.test",
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateId:                        app,
				ImportStateVerifyIdentifierAttribute: "name",
			},
			// Mutate displayname.
			{
				Config: fmt.Sprintf(`
resource "kanidm_group" "linked" {
  id = %q
}

resource "kanidm_application" "test" {
  name         = %q
  displayname  = "Renamed"
  linked_group = kanidm_group.linked.id
}
`, linked, app),
				Check: resource.TestCheckResourceAttr("kanidm_application.test", "displayname", "Renamed"),
			},
		},
	})
}
