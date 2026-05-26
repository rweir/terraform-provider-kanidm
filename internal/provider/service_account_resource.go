package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/ssoriche/terraform-provider-kanidm/internal/client"
)

var (
	_ resource.Resource                = (*serviceAccountResource)(nil)
	_ resource.ResourceWithImportState = (*serviceAccountResource)(nil)
)

func NewServiceAccountResource() resource.Resource {
	return &serviceAccountResource{}
}

type serviceAccountResource struct {
	client *client.Client
}

type serviceAccountResourceModel struct {
	ID             types.String `tfsdk:"id"`
	DisplayName    types.String `tfsdk:"displayname"`
	APIToken       types.String `tfsdk:"api_token"`
	EntryManagedBy types.Set    `tfsdk:"entry_managed_by"`
	MemberOf       types.Set    `tfsdk:"member_of"`
}

func (r *serviceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

func (r *serviceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a Kanidm service account.

Service accounts are used for automated systems and applications to authenticate with Kanidm.
An API token is automatically generated on creation and can be used for authentication.

## Example Usage

` + "```hcl" + `
resource "kanidm_service_account" "terraform" {
  id          = "terraform-automation"
  displayname = "Terraform Automation Account"
}

# Store the API token in 1Password or another secret manager
output "terraform_token" {
  value     = kanidm_service_account.terraform.api_token
  sensitive = true
}
` + "```" + `

**Important:** The API token is only available during creation and cannot be recovered later.
Store it securely immediately after creation.`,

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Unique identifier for the service account. Cannot be changed after creation.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"displayname": schema.StringAttribute{
				MarkdownDescription: "Display name for the service account. This is shown in the Kanidm UI and logs.",
				Required:            true,
			},
			"api_token": schema.StringAttribute{
				MarkdownDescription: "API token for the service account. **Only available during creation.** " +
					"Store this token securely as it cannot be retrieved later.",
				Computed:  true,
				Sensitive: true,
			},
			"entry_managed_by": schema.SetAttribute{
				MarkdownDescription: "Set of account or group IDs that can manage this service account. " +
					"This allows delegated administration, including API token generation. " +
					"**Required by Kanidm.** Use fully-qualified names (e.g., `terraform-admin@idm.s8i.ca`).",
				Required:    true,
				ElementType: types.StringType,
			},
			"member_of": schema.SetAttribute{
				MarkdownDescription: "Groups the service account is a member of. When declared, tofu " +
					"ensures the SA belongs to exactly these groups (adds missing, removes obsolete). " +
					"When undeclared, tofu doesn't touch the SA's memberships at all — useful for " +
					"adding the SA to kanidm builtin groups (like `idm_unix_authentication_read` or " +
					"`idm_mail_servers`) that we don't manage from the group side. Bare group names; " +
					"the SPN suffix is stripped on Read.",
				Optional:    true,
				ElementType: types.StringType,
			},
		},
	}
}

func (r *serviceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			"Expected *client.Client. Please report this issue to the provider developers.",
		)
		return
	}

	r.client = c
}

func (r *serviceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan serviceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Creating service account", map[string]any{
		"id":          plan.ID.ValueString(),
		"displayname": plan.DisplayName.ValueString(),
	})

	// Extract entry_managed_by (required)
	var entryManagedBy []string
	resp.Diagnostics.Append(plan.EntryManagedBy.ElementsAs(ctx, &entryManagedBy, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Create the service account (this also generates an initial API token)
	sa, err := r.client.CreateServiceAccount(ctx, plan.ID.ValueString(), plan.DisplayName.ValueString(), entryManagedBy)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating Service Account",
			"Could not create service account: "+err.Error(),
		)
		return
	}

	// Map response to state
	plan.ID = types.StringValue(sa.ID)
	plan.DisplayName = types.StringValue(sa.DisplayName)
	plan.APIToken = types.StringValue(sa.APIToken)

	// Set entry_managed_by
	if len(sa.EntryManagedBy) > 0 {
		embySet, diags := types.SetValueFrom(ctx, types.StringType, sa.EntryManagedBy)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.EntryManagedBy = embySet
	} else {
		plan.EntryManagedBy = types.SetNull(types.StringType)
	}

	// member_of: add the SA to each declared group, then mirror state
	// from the server. If undeclared in config, leave it Null so future
	// plans don't touch memberships (the SA may be in groups via other
	// means; we don't own those).
	if !plan.MemberOf.IsNull() && !plan.MemberOf.IsUnknown() {
		var wantGroups []string
		resp.Diagnostics.Append(plan.MemberOf.ElementsAs(ctx, &wantGroups, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		for _, g := range wantGroups {
			if err := r.client.AddMemberToGroup(ctx, g, sa.ID); err != nil {
				resp.Diagnostics.AddError(
					"Error Adding Service Account to Group",
					"Service account was created but could not be added to group "+g+": "+err.Error(),
				)
				return
			}
		}
		memberOfSet, diags := types.SetValueFrom(ctx, types.StringType, wantGroups)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.MemberOf = memberOfSet
	}

	tflog.Debug(ctx, "Service account created successfully", map[string]any{
		"id":          plan.ID.ValueString(),
		"displayname": plan.DisplayName.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serviceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state serviceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Reading service account", map[string]any{
		"id": state.ID.ValueString(),
	})

	// Get current service account from API
	sa, err := r.client.GetServiceAccount(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			tflog.Warn(ctx, "Service account not found, removing from state", map[string]any{
				"id": state.ID.ValueString(),
			})
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error Reading Service Account",
			"Could not read service account: "+err.Error(),
		)
		return
	}

	// Update state with current values
	state.ID = types.StringValue(sa.ID)
	state.DisplayName = types.StringValue(sa.DisplayName)

	// Set entry_managed_by
	if len(sa.EntryManagedBy) > 0 {
		embySet, diags := types.SetValueFrom(ctx, types.StringType, sa.EntryManagedBy)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		state.EntryManagedBy = embySet
	} else {
		state.EntryManagedBy = types.SetNull(types.StringType)
	}

	// member_of: state is authoritative — we trust what Create/Update
	// last wrote rather than mirroring from the server. Kanidm's
	// service_account GET response doesn't reliably return
	// `directmemberof`, so reading membership back would clobber
	// state with an empty set and produce a permanent diff/apply
	// cycle. Trade-off: out-of-band group additions or removals
	// won't be detected by refresh — accept that limitation.

	// API token is write-only and cannot be read back, preserve existing state value

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *serviceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state serviceAccountResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Updating service account", map[string]any{
		"id": plan.ID.ValueString(),
	})

	// Check if entry_managed_by has changed
	var entryManagedBy []string
	entryManagedByChanged := !plan.EntryManagedBy.Equal(state.EntryManagedBy)
	if entryManagedByChanged {
		if !plan.EntryManagedBy.IsNull() && !plan.EntryManagedBy.IsUnknown() {
			resp.Diagnostics.Append(plan.EntryManagedBy.ElementsAs(ctx, &entryManagedBy, false)...)
			if resp.Diagnostics.HasError() {
				return
			}
		} else {
			entryManagedBy = []string{} // Explicitly clear if set to null
		}

		tflog.Debug(ctx, "EntryManagedBy changed, updating service account", map[string]any{
			"id": plan.ID.ValueString(),
		})
	}

	// Check if displayname has changed
	displayNameChanged := !plan.DisplayName.Equal(state.DisplayName)

	// Only call UpdateServiceAccount if something changed
	if displayNameChanged || entryManagedByChanged {
		if displayNameChanged {
			tflog.Debug(ctx, "Displayname changed, updating service account", map[string]any{
				"id":              plan.ID.ValueString(),
				"old_displayname": state.DisplayName.ValueString(),
				"new_displayname": plan.DisplayName.ValueString(),
			})
		}

		// Pass nil for entryManagedBy if it hasn't changed
		var emby []string
		if entryManagedByChanged {
			emby = entryManagedBy
		} else {
			emby = nil
		}

		err := r.client.UpdateServiceAccount(
			ctx,
			plan.ID.ValueString(),
			plan.DisplayName.ValueString(),
			emby,
		)
		if err != nil {
			resp.Diagnostics.AddError(
				"Error Updating Service Account",
				"Could not update service account: "+err.Error(),
			)
			return
		}
	}

	// member_of: diff old vs new and add/remove deltas. If config has
	// dropped the declaration entirely (plan Null, state non-Null),
	// remove the SA from every group state had tracked.
	var oldMemberOf, newMemberOf []string
	if !state.MemberOf.IsNull() && !state.MemberOf.IsUnknown() {
		resp.Diagnostics.Append(state.MemberOf.ElementsAs(ctx, &oldMemberOf, false)...)
	}
	if !plan.MemberOf.IsNull() && !plan.MemberOf.IsUnknown() {
		resp.Diagnostics.Append(plan.MemberOf.ElementsAs(ctx, &newMemberOf, false)...)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	oldSet := make(map[string]struct{}, len(oldMemberOf))
	for _, g := range oldMemberOf {
		oldSet[g] = struct{}{}
	}
	newSet := make(map[string]struct{}, len(newMemberOf))
	for _, g := range newMemberOf {
		newSet[g] = struct{}{}
	}
	for g := range oldSet {
		if _, keep := newSet[g]; !keep {
			if err := r.client.RemoveMemberFromGroup(ctx, g, plan.ID.ValueString()); err != nil {
				resp.Diagnostics.AddError(
					"Error Removing Service Account from Group",
					"Could not remove service account from group "+g+": "+err.Error(),
				)
				return
			}
		}
	}
	for g := range newSet {
		if _, had := oldSet[g]; !had {
			if err := r.client.AddMemberToGroup(ctx, g, plan.ID.ValueString()); err != nil {
				resp.Diagnostics.AddError(
					"Error Adding Service Account to Group",
					"Could not add service account to group "+g+": "+err.Error(),
				)
				return
			}
		}
	}
	// Mirror the final desired state — plan if declared, Null if not.
	if !plan.MemberOf.IsNull() && !plan.MemberOf.IsUnknown() {
		memberOfSet, diags := types.SetValueFrom(ctx, types.StringType, newMemberOf)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.MemberOf = memberOfSet
	} else {
		plan.MemberOf = types.SetNull(types.StringType)
	}

	// Preserve API token (cannot be updated)
	plan.APIToken = state.APIToken

	tflog.Debug(ctx, "Service account updated successfully", map[string]any{
		"id": plan.ID.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *serviceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state serviceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Deleting service account", map[string]any{
		"id": state.ID.ValueString(),
	})

	// Delete the service account
	if err := r.client.DeleteServiceAccount(ctx, state.ID.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			tflog.Warn(ctx, "Service account not found during delete, removing from state", map[string]any{
				"id": state.ID.ValueString(),
			})
			return
		}

		resp.Diagnostics.AddError(
			"Error Deleting Service Account",
			"Could not delete service account: "+err.Error(),
		)
		return
	}

	tflog.Debug(ctx, "Service account deleted successfully", map[string]any{
		"id": state.ID.ValueString(),
	})
}

func (r *serviceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Use the ID directly as the import identifier
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)

	tflog.Debug(ctx, "Imported service account", map[string]any{
		"id": req.ID,
	})

	// Add a warning about the API token
	resp.Diagnostics.AddWarning(
		"API Token Not Available",
		"The API token for this service account is not available after import. "+
			"If you need the token, you must regenerate it manually using the Kanidm CLI or web interface.",
	)
}
