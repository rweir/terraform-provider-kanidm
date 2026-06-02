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
	_ resource.Resource                = (*groupAccountPolicyResource)(nil)
	_ resource.ResourceWithImportState = (*groupAccountPolicyResource)(nil)
)

func NewGroupAccountPolicyResource() resource.Resource {
	return &groupAccountPolicyResource{}
}

type groupAccountPolicyResource struct {
	client *client.Client
}

type groupAccountPolicyResourceModel struct {
	Group             types.String `tfsdk:"group"`
	AuthSessionExpiry types.Int64  `tfsdk:"auth_session_expiry"`
}

func (r *groupAccountPolicyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_group_account_policy"
}

func (r *groupAccountPolicyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages Kanidm account-policy attributes on an existing group.

This resource does not create or delete the group. On create/update it ensures the group has
Kanidm's ` + "`account_policy`" + ` class, then applies the configured policy attributes. On delete,
it resets the explicit policy attributes but leaves the group and class in place.`,
		Attributes: map[string]schema.Attribute{
			"group": schema.StringAttribute{
				MarkdownDescription: "Name of the existing Kanidm group that should carry account-policy attributes.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"auth_session_expiry": schema.Int64Attribute{
				MarkdownDescription: "Authentication session lifetime in seconds for members of this group. " +
					"Maps to Kanidm's `authsession_expiry` account-policy attribute. Kanidm resolves " +
					"multiple applicable account policies by taking the shortest expiry.",
				Optional: true,
			},
		},
	}
}

func (r *groupAccountPolicyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *groupAccountPolicyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan groupAccountPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupName := plan.Group.ValueString()
	tflog.Debug(ctx, "Creating group account policy", map[string]any{"group": groupName})

	if _, err := r.ensureGroupAccountPolicy(ctx, groupName); err != nil {
		resp.Diagnostics.AddError(
			"Error Enabling Group Account Policy",
			"Could not enable account policy on group: "+err.Error(),
		)
		return
	}

	if !plan.AuthSessionExpiry.IsNull() && !plan.AuthSessionExpiry.IsUnknown() {
		if err := r.client.SetGroupAuthSessionExpiry(ctx, groupName, plan.AuthSessionExpiry.ValueInt64()); err != nil {
			resp.Diagnostics.AddError(
				"Error Setting Auth Session Expiry",
				"Could not set group auth session expiry: "+err.Error(),
			)
			return
		}
	}

	group, err := r.client.GetGroup(ctx, groupName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Group Account Policy",
			"Group account policy was updated but could not be read back: "+err.Error(),
		)
		return
	}

	r.readGroupAccountPolicyIntoState(group, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupAccountPolicyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state groupAccountPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupName := state.Group.ValueString()
	group, err := r.client.GetGroup(ctx, groupName)
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError(
			"Error Reading Group Account Policy",
			"Could not read group account policy: "+err.Error(),
		)
		return
	}

	r.readGroupAccountPolicyIntoState(group, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *groupAccountPolicyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state groupAccountPolicyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupName := plan.Group.ValueString()
	tflog.Debug(ctx, "Updating group account policy", map[string]any{"group": groupName})

	if _, err := r.ensureGroupAccountPolicy(ctx, groupName); err != nil {
		resp.Diagnostics.AddError(
			"Error Enabling Group Account Policy",
			"Could not enable account policy on group: "+err.Error(),
		)
		return
	}

	if !plan.AuthSessionExpiry.IsNull() && !plan.AuthSessionExpiry.IsUnknown() {
		if err := r.client.SetGroupAuthSessionExpiry(ctx, groupName, plan.AuthSessionExpiry.ValueInt64()); err != nil {
			resp.Diagnostics.AddError(
				"Error Setting Auth Session Expiry",
				"Could not set group auth session expiry: "+err.Error(),
			)
			return
		}
	} else if !state.AuthSessionExpiry.IsNull() {
		if err := r.client.ResetGroupAuthSessionExpiry(ctx, groupName); err != nil {
			resp.Diagnostics.AddError(
				"Error Resetting Auth Session Expiry",
				"Could not reset group auth session expiry: "+err.Error(),
			)
			return
		}
	}

	group, err := r.client.GetGroup(ctx, groupName)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Group Account Policy",
			"Group account policy was updated but could not be read back: "+err.Error(),
		)
		return
	}

	r.readGroupAccountPolicyIntoState(group, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *groupAccountPolicyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state groupAccountPolicyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupName := state.Group.ValueString()
	tflog.Debug(ctx, "Deleting group account policy", map[string]any{"group": groupName})

	if !state.AuthSessionExpiry.IsNull() {
		if err := r.client.ResetGroupAuthSessionExpiry(ctx, groupName); err != nil {
			if errors.Is(err, client.ErrNotFound) {
				return
			}
			resp.Diagnostics.AddError(
				"Error Resetting Auth Session Expiry",
				"Could not reset group auth session expiry: "+err.Error(),
			)
			return
		}
	}
}

func (r *groupAccountPolicyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("group"), req, resp)
}

func (r *groupAccountPolicyResource) ensureGroupAccountPolicy(ctx context.Context, groupName string) (*client.Group, error) {
	group, err := r.client.GetGroup(ctx, groupName)
	if err != nil {
		return nil, err
	}

	if group.AccountPolicy {
		return group, nil
	}

	if err := r.client.EnableGroupAccountPolicy(ctx, groupName); err != nil {
		return nil, err
	}

	return r.client.GetGroup(ctx, groupName)
}

func (r *groupAccountPolicyResource) readGroupAccountPolicyIntoState(group *client.Group, state *groupAccountPolicyResourceModel) {
	if group.ID != "" {
		state.Group = types.StringValue(group.ID)
	}
	if !state.AuthSessionExpiry.IsNull() || group.AuthSessionExpirySet {
		if group.AuthSessionExpirySet {
			state.AuthSessionExpiry = types.Int64Value(group.AuthSessionExpiry)
		} else {
			state.AuthSessionExpiry = types.Int64Null()
		}
	}
}
