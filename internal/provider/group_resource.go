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
	_ resource.Resource                = (*groupResource)(nil)
	_ resource.ResourceWithImportState = (*groupResource)(nil)
)

func NewGroupResource() resource.Resource {
	return &groupResource{}
}

type groupResource struct {
	client *client.Client
}

type groupResourceModel struct {
	ID          types.String `tfsdk:"id"`
	Description types.String `tfsdk:"description"`
	Members     types.Set    `tfsdk:"members"`
	Posix       types.Bool   `tfsdk:"posix"`
	GidNumber   types.Int64  `tfsdk:"gidnumber"`
}

func (r *groupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group"
}

func (r *groupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a Kanidm group.

Groups are used to organize users and service accounts, and control access to resources.

## Example Usage

` + "```hcl" + `
resource "kanidm_group" "developers" {
  id          = "developers"
  description = "Development team members"

  members = [
    kanidm_person.alice.id,
    kanidm_person.bob.id,
    kanidm_service_account.ci.id,
  ]
}
` + "```" + ``,

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Unique identifier for the group (group name). Cannot be changed after creation.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"description": schema.StringAttribute{
				MarkdownDescription: "Description of the group.",
				Optional:            true,
			},
			"members": schema.SetAttribute{
				MarkdownDescription: "Set of member IDs (persons or service accounts). " +
					"Members are managed as a complete set - any changes will replace all members.",
				Optional:    true,
				ElementType: types.StringType,
			},
			"posix": schema.BoolAttribute{
				MarkdownDescription: "If true, mark the group as a POSIX group (adds the `posixgroup` " +
					"class and assigns a gidnumber). Required for groups whose membership should be " +
					"visible via NSS/`kanidm_unixd`. " +
					"**Kanidm doesn't support unsetting this once enabled** — flipping " +
					"this back to false is an error.",
				Optional: true,
			},
			"gidnumber": schema.Int64Attribute{
				MarkdownDescription: "POSIX gidnumber assigned by Kanidm when `posix = true`. " +
					"Auto-assigned from the entry's UUID; not user-settable.",
				Computed: true,
			},
		},
	}
}

func (r *groupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *groupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Creating group", map[string]any{
		"id": plan.ID.ValueString(),
	})

	// Create the group
	description := ""
	if !plan.Description.IsNull() {
		description = plan.Description.ValueString()
	}

	group, err := r.client.CreateGroup(ctx, plan.ID.ValueString(), description)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating Group",
			"Could not create group: "+err.Error(),
		)
		return
	}

	// Add members if provided
	if !plan.Members.IsNull() && !plan.Members.IsUnknown() {
		var memberIDs []string
		resp.Diagnostics.Append(plan.Members.ElementsAs(ctx, &memberIDs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}

		if len(memberIDs) > 0 {
			tflog.Debug(ctx, "Adding members to group", map[string]any{
				"count": len(memberIDs),
			})
			if err := r.client.UpdateGroup(ctx, group.ID, "", memberIDs); err != nil {
				resp.Diagnostics.AddError(
					"Error Adding Members",
					"Group was created but members could not be added: "+err.Error(),
				)
				return
			}
		}
	}

	// Enable POSIX if requested. Kanidm assigns the gidnumber itself.
	if plan.Posix.ValueBool() {
		tflog.Debug(ctx, "Enabling POSIX on group", map[string]any{"id": group.ID})
		if err := r.client.EnableGroupPosix(ctx, group.ID); err != nil {
			resp.Diagnostics.AddError(
				"Error Enabling POSIX",
				"Group was created but POSIX could not be enabled: "+err.Error(),
			)
			return
		}
	}

	// Read back the created group
	createdGroup, err := r.client.GetGroup(ctx, group.ID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Group",
			"Group was created but could not be read back: "+err.Error(),
		)
		return
	}

	// Map response to state. Preserve null-vs-set distinction:
	// Terraform's framework requires the new state to match the
	// plan where the plan was concrete. A plan with `description`
	// or `members` unset arrives as Null; we must keep that Null
	// rather than turning it into "" / an empty Set, even if the
	// API trivially returns either.
	plan.ID = types.StringValue(createdGroup.ID)
	if !plan.Description.IsNull() || createdGroup.Description != "" {
		plan.Description = types.StringValue(createdGroup.Description)
	}
	if !plan.Members.IsNull() || len(createdGroup.Members) > 0 {
		membersSet, diags := types.SetValueFrom(ctx, types.StringType, createdGroup.Members)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.Members = membersSet
	}

	// Mirror POSIX state from the server. gidnumber is always exposed
	// when posix is set; when posix is null in plan AND the server
	// doesn't have it set either, leave both Null/Unknown alone.
	if !plan.Posix.IsNull() || createdGroup.Posix {
		plan.Posix = types.BoolValue(createdGroup.Posix)
	}
	if createdGroup.Posix {
		plan.GidNumber = types.Int64Value(createdGroup.GidNumber)
	} else {
		plan.GidNumber = types.Int64Null()
	}

	tflog.Debug(ctx, "Group created successfully", map[string]any{
		"id": plan.ID.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Reading group", map[string]any{
		"id": state.ID.ValueString(),
	})

	// Get current group from API
	group, err := r.client.GetGroup(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			tflog.Warn(ctx, "Group not found, removing from state", map[string]any{
				"id": state.ID.ValueString(),
			})
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error Reading Group",
			"Could not read group: "+err.Error(),
		)
		return
	}

	// Update state with current values. Preserve null-vs-set: if the
	// API returns a real description / member list, mirror it; if
	// it's empty AND the existing state was null (user didn't ask
	// for it), keep state null so refresh doesn't introduce drift.
	state.ID = types.StringValue(group.ID)
	if !state.Description.IsNull() || group.Description != "" {
		state.Description = types.StringValue(group.Description)
	}
	if !state.Members.IsNull() || len(group.Members) > 0 {
		membersSet, diags := types.SetValueFrom(ctx, types.StringType, group.Members)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		state.Members = membersSet
	}

	// Mirror POSIX state from the server. Preserve Null when neither
	// existing state nor server has it set.
	if !state.Posix.IsNull() || group.Posix {
		state.Posix = types.BoolValue(group.Posix)
	}
	if group.Posix {
		state.GidNumber = types.Int64Value(group.GidNumber)
	} else {
		state.GidNumber = types.Int64Null()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *groupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Updating group", map[string]any{
		"id": plan.ID.ValueString(),
	})

	// Prepare members list
	var memberIDs []string
	if !plan.Members.IsNull() && !plan.Members.IsUnknown() {
		resp.Diagnostics.Append(plan.Members.ElementsAs(ctx, &memberIDs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// Update group (description and members)
	description := ""
	if !plan.Description.IsNull() {
		description = plan.Description.ValueString()
	}

	// POSIX transitions are checked first so a refuse-to-disable
	// diagnostic surfaces before any other failure (e.g. an empty
	// PATCH against UpdateGroup when nothing else changes).
	//   - null/false -> true: POST /_unix to enable (deferred until
	//     after UpdateGroup so creation order in state is sensible)
	//   - true -> false: refuse; kanidm doesn't support disabling
	//   - any -> same: no-op
	wasPosix := state.Posix.ValueBool()
	wantPosix := plan.Posix.ValueBool()
	if wasPosix && !wantPosix {
		resp.Diagnostics.AddAttributeError(
			path.Root("posix"),
			"POSIX cannot be disabled",
			"Kanidm doesn't support removing the `posixgroup` class once it has been set. "+
				"To remove POSIX status, the group has to be destroyed and re-created.",
		)
		return
	}

	if err := r.client.UpdateGroup(ctx, plan.ID.ValueString(), description, memberIDs); err != nil {
		resp.Diagnostics.AddError(
			"Error Updating Group",
			"Could not update group: "+err.Error(),
		)
		return
	}

	if !wasPosix && wantPosix {
		tflog.Debug(ctx, "Enabling POSIX on group", map[string]any{"id": plan.ID.ValueString()})
		if err := r.client.EnableGroupPosix(ctx, plan.ID.ValueString()); err != nil {
			resp.Diagnostics.AddError(
				"Error Enabling POSIX",
				"Could not enable POSIX on group: "+err.Error(),
			)
			return
		}
	}

	// Read back the updated group
	updatedGroup, err := r.client.GetGroup(ctx, plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Group",
			"Group was updated but could not be read back: "+err.Error(),
		)
		return
	}

	// Update state (same null-preservation logic as Create).
	plan.ID = types.StringValue(updatedGroup.ID)
	if !plan.Description.IsNull() || updatedGroup.Description != "" {
		plan.Description = types.StringValue(updatedGroup.Description)
	}
	if !plan.Members.IsNull() || len(updatedGroup.Members) > 0 {
		membersSet, diags := types.SetValueFrom(ctx, types.StringType, updatedGroup.Members)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.Members = membersSet
	}
	if !plan.Posix.IsNull() || updatedGroup.Posix {
		plan.Posix = types.BoolValue(updatedGroup.Posix)
	}
	if updatedGroup.Posix {
		plan.GidNumber = types.Int64Value(updatedGroup.GidNumber)
	} else {
		plan.GidNumber = types.Int64Null()
	}

	tflog.Debug(ctx, "Group updated successfully", map[string]any{
		"id": plan.ID.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Deleting group", map[string]any{
		"id": state.ID.ValueString(),
	})

	// Delete the group
	if err := r.client.DeleteGroup(ctx, state.ID.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			tflog.Warn(ctx, "Group not found during delete, removing from state", map[string]any{
				"id": state.ID.ValueString(),
			})
			return
		}

		resp.Diagnostics.AddError(
			"Error Deleting Group",
			"Could not delete group: "+err.Error(),
		)
		return
	}

	tflog.Debug(ctx, "Group deleted successfully", map[string]any{
		"id": state.ID.ValueString(),
	})
}

func (r *groupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Use the ID (group name) directly as the import identifier
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)

	tflog.Debug(ctx, "Imported group", map[string]any{
		"id": req.ID,
	})
}
