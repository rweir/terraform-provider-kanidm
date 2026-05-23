//go:build acc

package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Round-trip the POSIX flag on a group: create with posix=true,
// confirm gidnumber lands in state, refresh as a no-op, and import
// back identically. The kanidm CLI equivalent is:
//
//	kanidm group posix set <name>
//
// which POSTs to /v1/group/<name>/_unix with no body and lets the
// server auto-assign a gidnumber from the entry's UUID.
func TestAccGroup_posix(t *testing.T) {
	name := uniq("group-posix")

	cfg := fmt.Sprintf(`
resource "kanidm_group" "test" {
  id    = %q
  posix = true
}
`, name)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create with posix=true.
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_group.test", "posix", "true"),
					// gidnumber is computed by Kanidm.
					resource.TestMatchResourceAttr("kanidm_group.test", "gidnumber", regexp.MustCompile(`^\d+$`)),
				),
			},
			// Refresh — no drift.
			{
				Config:   cfg,
				PlanOnly: true,
			},
			// Import via the kanidm group name; state should match.
			{
				ResourceName:                         "kanidm_group.test",
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateId:                        name,
				ImportStateVerifyIdentifierAttribute: "id",
			},
		},
	})
}

// Explicit gidnumber on POSIX enable. Useful for kanidm rebuilds
// where existing on-disk file ownership needs to be preserved.
// Kanidm accepts a `gidnumber` field in the POST /v1/group/<name>/_unix
// body, overriding the default auto-assignment from the entry UUID.
//
// The valid range for explicit GIDs is roughly 65536–524287; we
// pick something inside it.
func TestAccGroup_posixExplicitGid(t *testing.T) {
	name := uniq("group-posix-gid")
	const gid = 200042

	cfg := fmt.Sprintf(`
resource "kanidm_group" "test" {
  id        = %q
  posix     = true
  gidnumber = %d
}
`, name, gid)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: cfg,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_group.test", "posix", "true"),
					resource.TestCheckResourceAttr("kanidm_group.test", "gidnumber", fmt.Sprintf("%d", gid)),
				),
			},
			{
				Config:   cfg,
				PlanOnly: true,
			},
			{
				ResourceName:                         "kanidm_group.test",
				ImportState:                          true,
				ImportStateVerify:                    true,
				ImportStateId:                        name,
				ImportStateVerifyIdentifierAttribute: "id",
			},
		},
	})
}

// Kanidm doesn't support removing the POSIX class from a group once
// it's been set. The provider should refuse the transition with a
// clear diagnostic rather than silently failing.
func TestAccGroup_posixCannotDisable(t *testing.T) {
	name := uniq("group-posix-stuck")

	enable := fmt.Sprintf(`
resource "kanidm_group" "test" {
  id    = %q
  posix = true
}
`, name)
	disable := fmt.Sprintf(`
resource "kanidm_group" "test" {
  id    = %q
  posix = false
}
`, name)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{Config: enable},
			{
				Config:      disable,
				ExpectError: regexp.MustCompile(`(?i)posix.*cannot.*disable|kanidm doesn't support`),
			},
		},
	})
}
