//go:build acc

// Shared scaffolding for acceptance tests. Tests are gated by both
// the `acc` build tag and the standard `TF_ACC=1` env var.
//
// Bring up a local Kanidm with `make test-acc-up`, then run:
//
//	source test/.env
//	make test-acc

package provider_test

import (
	"fmt"
	"os"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/ssoriche/terraform-provider-kanidm/internal/provider"
)

// providerName is the local name we use for the provider in the
// `required_providers` block of each test config. Keep it short.
const providerName = "kanidm"

// testAccProtoV6ProviderFactories is the provider factory map every
// acceptance test passes to resource.Test. Building the provider in
// process avoids the binary install + dev_overrides dance for tests.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	providerName: providerserver.NewProtocol6WithError(provider.New("acc")()),
}

// testAccPreCheck fails the test fast if the environment isn't set up
// — KANIDM_URL and KANIDM_TOKEN must point at a running Kanidm.
func testAccPreCheck(t *testing.T) {
	t.Helper()
	if os.Getenv("KANIDM_URL") == "" {
		t.Fatal("KANIDM_URL must be set for acceptance tests (try: make test-acc-up && source test/.env)")
	}
	if os.Getenv("KANIDM_TOKEN") == "" {
		t.Fatal("KANIDM_TOKEN must be set for acceptance tests (try: make test-acc-up && source test/.env)")
	}
}

// uniq returns a short suffix that's unique across one test binary
// run, so tests can construct resource names like
// `fmt.Sprintf("tf-acc-%s-%s", suite, uniq("group"))` and not collide
// when run in parallel against the same Kanidm.
var uniqCounter atomic.Uint64

func uniq(label string) string {
	n := uniqCounter.Add(1)
	return fmt.Sprintf("%s-%d-%s", label, time.Now().UnixNano()%1_000_000, strconv.FormatUint(n, 36))
}
