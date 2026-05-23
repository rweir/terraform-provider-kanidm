//go:build acc

package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Service-account create + refresh + destroy. entry_managed_by is
// the only required-by-Kanidm field; we set it to idm_admins (which
// always exists in a fresh Kanidm).
func TestAccServiceAccount_basic(t *testing.T) {
	name := uniq("sa")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "kanidm_service_account" "test" {
  id               = %q
  displayname      = "TF Acceptance Test SA"
  entry_managed_by = ["idm_admins@idm.localhost"]
}
`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_service_account.test", "id", name),
					resource.TestCheckResourceAttr("kanidm_service_account.test", "displayname", "TF Acceptance Test SA"),
					resource.TestCheckResourceAttrSet("kanidm_service_account.test", "api_token"),
				),
			},
			{
				Config: fmt.Sprintf(`
resource "kanidm_service_account" "test" {
  id               = %q
  displayname      = "TF Acceptance Test SA"
  entry_managed_by = ["idm_admins@idm.localhost"]
}
`, name),
				PlanOnly: true,
			},
		},
	})
}
