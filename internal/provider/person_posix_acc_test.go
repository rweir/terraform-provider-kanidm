//go:build acc

package provider_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Round-trip POSIX on a person account: create with posix=true plus
// optional gidnumber and loginshell, refresh, import, mutate shell,
// then drop posix from config entirely — should be a no-op (Rob's
// "if not declared in tofu, leave server alone" rule, enforced by
// the Optional+Computed+UseStateForUnknown plan modifier).
func TestAccPerson_posix(t *testing.T) {
	id := uniq("person-posix")
	const gid = 250042

	cfgFull := func(shell string) string {
		return fmt.Sprintf(`
resource "kanidm_person" "test" {
  id          = %q
  displayname = "TF POSIX Test"
  posix       = true
  gidnumber   = %d
  loginshell  = %q
}
`, id, gid, shell)
	}

	cfgNoPosix := fmt.Sprintf(`
resource "kanidm_person" "test" {
  id          = %q
  displayname = "TF POSIX Test"
}
`, id)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create with full POSIX configuration.
			{
				Config: cfgFull("/bin/zsh"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_person.test", "posix", "true"),
					resource.TestCheckResourceAttr("kanidm_person.test", "gidnumber", fmt.Sprintf("%d", gid)),
					resource.TestCheckResourceAttr("kanidm_person.test", "loginshell", "/bin/zsh"),
				),
			},
			// Refresh: no drift.
			{
				Config:   cfgFull("/bin/zsh"),
				PlanOnly: true,
			},
			// Import round-trip.
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
			// Mutate the shell. (POSIX itself stays true, gidnumber
			// stays pinned.)
			{
				Config: cfgFull("/bin/bash"),
				Check: resource.TestCheckResourceAttr("kanidm_person.test", "loginshell", "/bin/bash"),
			},
			{
				Config:   cfgFull("/bin/bash"),
				PlanOnly: true,
			},
			// Drop posix/gidnumber/loginshell from config. Server
			// still has all three; tofu must leave them alone.
			{
				Config:   cfgNoPosix,
				PlanOnly: true,
			},
		},
	})
}

// Kanidm doesn't support removing the `posixaccount` class once set —
// same as posixgroup. Refuse explicit `posix = false` after enable.
func TestAccPerson_posixCannotDisable(t *testing.T) {
	id := uniq("person-posix-stuck")

	enable := fmt.Sprintf(`
resource "kanidm_person" "test" {
  id          = %q
  displayname = "Stuck POSIX"
  posix       = true
}
`, id)
	disable := fmt.Sprintf(`
resource "kanidm_person" "test" {
  id          = %q
  displayname = "Stuck POSIX"
  posix       = false
}
`, id)

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
