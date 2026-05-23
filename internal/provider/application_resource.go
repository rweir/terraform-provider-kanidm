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
	_ resource.Resource                = (*applicationResource)(nil)
	_ resource.ResourceWithImportState = (*applicationResource)(nil)
)

func NewApplicationResource() resource.Resource {
	return &applicationResource{}
}

type applicationResource struct {
	client *client.Client
}

type applicationResourceModel struct {
	Name        types.String `tfsdk:"name"`
	DisplayName types.String `tfsdk:"displayname"`
	LinkedGroup types.String `tfsdk:"linked_group"`
	UUID        types.String `tfsdk:"uuid"`
}

func (r *applicationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_application"
}

func (r *applicationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a Kanidm Application service account.

Application service accounts gate per-user application passwords —
they're how Kanidm models "this user has an IMAP-specific credential
to authenticate as themselves to the IMAP server". The application
references a kanidm group; only members of that group may mint
application passwords for it (see ` + "`kanidm_application_password`" + `).

These are managed via Kanidm's SCIM v1 API (` + "`/scim/v1/Application`" + `),
distinct from plain ` + "`kanidm_service_account`" + ` (which can't model
the ` + "`linked_group`" + ` attribute).`,
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				MarkdownDescription: "Unique application name. Cannot be changed after creation.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"displayname": schema.StringAttribute{
				MarkdownDescription: "Display name for the application.",
				Required:            true,
			},
			"linked_group": schema.StringAttribute{
				MarkdownDescription: "Name of the Kanidm group whose members may mint application " +
					"passwords for this application.",
				Required: true,
			},
			"uuid": schema.StringAttribute{
				MarkdownDescription: "Kanidm-assigned UUID. Use this when referencing the application " +
					"from `kanidm_application_password.application_uuid`.",
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

func (r *applicationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *applicationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan applicationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Creating application", map[string]any{"name": plan.Name.ValueString()})

	app, err := r.client.CreateApplication(
		ctx,
		plan.Name.ValueString(),
		plan.DisplayName.ValueString(),
		plan.LinkedGroup.ValueString(),
	)
	if err != nil {
		resp.Diagnostics.AddError("Error Creating Application", err.Error())
		return
	}

	plan.Name = types.StringValue(app.Name)
	plan.DisplayName = types.StringValue(app.DisplayName)
	plan.LinkedGroup = types.StringValue(app.LinkedGroup)
	plan.UUID = types.StringValue(app.ID)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *applicationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state applicationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	app, err := r.client.GetApplication(ctx, state.Name.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Error Reading Application", err.Error())
		return
	}

	state.Name = types.StringValue(app.Name)
	state.DisplayName = types.StringValue(app.DisplayName)
	state.LinkedGroup = types.StringValue(app.LinkedGroup)
	state.UUID = types.StringValue(app.ID)

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *applicationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state applicationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var dn, lg *string
	if !plan.DisplayName.Equal(state.DisplayName) {
		v := plan.DisplayName.ValueString()
		dn = &v
	}
	if !plan.LinkedGroup.Equal(state.LinkedGroup) {
		v := plan.LinkedGroup.ValueString()
		lg = &v
	}

	if dn != nil || lg != nil {
		// Use state.UUID — SCIM PUT addresses by uuid (`id`).
		if err := r.client.UpdateApplication(ctx, state.UUID.ValueString(), dn, lg); err != nil {
			resp.Diagnostics.AddError("Error Updating Application", err.Error())
			return
		}
	}

	updated, err := r.client.GetApplication(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Application", err.Error())
		return
	}
	plan.Name = types.StringValue(updated.Name)
	plan.DisplayName = types.StringValue(updated.DisplayName)
	plan.LinkedGroup = types.StringValue(updated.LinkedGroup)
	plan.UUID = types.StringValue(updated.ID)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *applicationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state applicationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteApplication(ctx, state.Name.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			return
		}
		resp.Diagnostics.AddError("Error Deleting Application", err.Error())
		return
	}
}

func (r *applicationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)
}
