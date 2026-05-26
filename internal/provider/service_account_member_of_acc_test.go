//go:build acc

package provider_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// Round-trip a service account's `member_of` set: create it as a
// member of one group, refresh as no-op, switch to a different group
// (provider should add the new one and remove the old), drop the
// declaration entirely (provider should remove the SA from the last
// declared groups).
//
// Uses two non-high-privilege kanidm builtin groups
// (`idm_unix_authentication_read` and `idm_mail_servers`) which
// exactly mirror the production use case (unix-bind → first,
// mailers-dump → second). They're builtin so we don't manage them
// via kanidm_group, avoiding the cross-resource conflict on the
// group's `members` attribute. Critically, neither is in
// `idm_high_privilege`, so the test-cleanup destroy of the SA
// doesn't require elevation (which the test harness's idm_admin
// session can't provide).
//
// Memberships are managed via a read-modify-write PATCH on the
// group's `member` attribute, so any other existing members of those
// builtin groups in the test instance are preserved.
func TestAccServiceAccount_memberOf(t *testing.T) {
	const g1 = "idm_unix_authentication_read"
	const g2 = "idm_mail_servers"
	sa := uniq("sa-memberof")

	with := func(memberOf string) string {
		// memberOf is a literal HCL fragment, e.g. `member_of = [...]` or "".
		return fmt.Sprintf(`
resource "kanidm_service_account" "test" {
  id               = %q
  displayname      = "TF member_of test"
  entry_managed_by = ["idm_admins@idm.localhost"]
%s
}
`, sa, memberOf)
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create with one membership.
			{
				Config: with(fmt.Sprintf(`  member_of = [%q]`, g1)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_service_account.test", "member_of.#", "1"),
					resource.TestCheckTypeSetElemAttr("kanidm_service_account.test", "member_of.*", g1),
				),
			},
			{
				Config:   with(fmt.Sprintf(`  member_of = [%q]`, g1)),
				PlanOnly: true,
			},
			// Swap membership.
			{
				Config: with(fmt.Sprintf(`  member_of = [%q]`, g2)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("kanidm_service_account.test", "member_of.#", "1"),
					resource.TestCheckTypeSetElemAttr("kanidm_service_account.test", "member_of.*", g2),
				),
			},
			{
				Config:   with(fmt.Sprintf(`  member_of = [%q]`, g2)),
				PlanOnly: true,
			},
			// Hold both.
			{
				Config: with(fmt.Sprintf(`  member_of = [%q, %q]`, g1, g2)),
				Check: resource.TestCheckResourceAttr("kanidm_service_account.test", "member_of.#", "2"),
			},
			{
				Config:   with(fmt.Sprintf(`  member_of = [%q, %q]`, g1, g2)),
				PlanOnly: true,
			},
			// Drop the declaration. State had [g1, g2]; tofu must
			// remove the SA from both groups, since those memberships
			// were under tofu management and the user has withdrawn
			// the declaration.
			{
				Config: with(``),
				Check: resource.TestCheckNoResourceAttr("kanidm_service_account.test", "member_of.#"),
			},
			// After the apply, the next refresh+plan must be clean —
			// state.MemberOf is Null and config has no declaration.
			{
				Config:   with(``),
				PlanOnly: true,
			},
		},
	})
}
