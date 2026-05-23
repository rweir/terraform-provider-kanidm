//go:build acc

package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Basic create / drift-free-refresh / destroy lifecycle for a group
// with no members.
func TestAccGroup_basic(t *testing.T) {
	name := uniq("group")
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "kanidm_group" "test" {
  id          = %q
  description = "tf acceptance test"
}
`, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_group.test", "id", name),
					resource.TestCheckResourceAttr("kanidm_group.test", "description", "tf acceptance test"),
				),
			},
			// Re-apply with no changes — should be a no-op (verifies
			// Read + diff is stable, not just create-then-destroy).
			{
				Config: fmt.Sprintf(`
resource "kanidm_group" "test" {
  id          = %q
  description = "tf acceptance test"
}
`, name),
				PlanOnly: true,
			},
		},
	})
}
