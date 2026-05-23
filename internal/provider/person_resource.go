package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	"github.com/ssoriche/terraform-provider-kanidm/internal/client"
)

// Ensure the implementation satisfies the required interfaces
var (
	_ resource.Resource                = (*personResource)(nil)
	_ resource.ResourceWithImportState = (*personResource)(nil)
)

// NewPersonResource creates a new person resource
func NewPersonResource() resource.Resource {
	return &personResource{}
}

// personResource is the resource implementation
type personResource struct {
	client *client.Client
}

// personResourceModel describes the resource data model
type personResourceModel struct {
	ID                           types.String `tfsdk:"id"`
	DisplayName                  types.String `tfsdk:"displayname"`
	Mail                         types.List   `tfsdk:"mail"`
	Legalname                    types.String `tfsdk:"legalname"`
	Posix                        types.Bool   `tfsdk:"posix"`
	GidNumber                    types.Int64  `tfsdk:"gidnumber"`
	Loginshell                   types.String `tfsdk:"loginshell"`
	Password                     types.String `tfsdk:"password"`
	GenerateCredentialResetToken types.Bool   `tfsdk:"generate_credential_reset_token"`
	CredentialResetToken         types.String `tfsdk:"credential_reset_token"`
	CredentialResetTokenTTL      types.Int64  `tfsdk:"credential_reset_token_ttl"`
}

// Metadata returns the resource type name
func (r *personResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_person"
}

// Schema defines the schema for the resource
func (r *personResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a Kanidm person account.

## Authentication Setup

Kanidm supports two credential setup workflows:

### Password-Based Authentication
Set the ` + "`password`" + ` attribute to create a password-based account:

` + "```hcl" + `
resource "kanidm_person" "example" {
  id          = "jdoe"
  displayname = "John Doe"
  password    = var.initial_password
}
` + "```" + `

### Passkey/Modern Authentication (Recommended)
Set ` + "`generate_credential_reset_token = true`" + ` to generate a one-time token for credential setup via the Kanidm web UI:

` + "```hcl" + `
resource "kanidm_person" "example" {
  id                            = "jdoe"
  displayname                   = "John Doe"
  generate_credential_reset_token = true
}

output "credential_reset_token" {
  value     = kanidm_person.example.credential_reset_token
  sensitive = true
}
` + "```" + `

The user can then visit the Kanidm web UI with the token to set up passkeys or passwords.`,

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				MarkdownDescription: "Unique identifier for the person account (username). Cannot be changed after creation.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"displayname": schema.StringAttribute{
				MarkdownDescription: "Display name of the person.",
				Required:            true,
			},
			"mail": schema.ListAttribute{
				MarkdownDescription: "Email addresses for the person.",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"legalname": schema.StringAttribute{
				MarkdownDescription: "Legal name of the person. " +
					"If unset in config, the provider leaves whatever value the server holds " +
					"untouched — so users can change their legalname out-of-band via the kanidm " +
					"CLI without tofu trying to revert.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"posix": schema.BoolAttribute{
				MarkdownDescription: "If true, the account has the `posixaccount` class (visible via " +
					"NSS / `kanidm_unixd`). When unset in config, the provider leaves the server's " +
					"current POSIX state alone — users can enable POSIX out-of-band without tofu " +
					"reverting. **Kanidm doesn't support unsetting this once enabled** — flipping " +
					"true to false is an error.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.Bool{
					boolplanmodifier.UseStateForUnknown(),
				},
			},
			"gidnumber": schema.Int64Attribute{
				MarkdownDescription: "POSIX gidnumber for the account. When `posix = true` and this " +
					"is unset, Kanidm auto-assigns one from the entry's UUID. Set explicitly to " +
					"pin a specific gid — useful when rebuilding a Kanidm instance and the account " +
					"already owns files on disk.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"loginshell": schema.StringAttribute{
				MarkdownDescription: "POSIX login shell, e.g. `/bin/zsh`. When unset in config, the " +
					"provider leaves the server value alone — users can change their shell " +
					"out-of-band without tofu reverting.",
				Optional: true,
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"password": schema.StringAttribute{
				MarkdownDescription: "Password for the person account. **Note:** This is write-only and will not be stored in state. " +
					"Mutually exclusive with `generate_credential_reset_token`. " +
					"Consider using `lifecycle { ignore_changes = [password] }` if the password is managed externally.",
				Optional:  true,
				Sensitive: true,
			},
			"generate_credential_reset_token": schema.BoolAttribute{
				MarkdownDescription: "Whether to generate a credential reset token for passkey/password setup via the web UI. " +
					"Mutually exclusive with `password`. Defaults to `false`.",
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(false),
			},
			"credential_reset_token": schema.StringAttribute{
				MarkdownDescription: "The credential reset token (generated when `generate_credential_reset_token` is `true`). " +
					"This token can be used once to set up credentials via the Kanidm web UI. **Computed value only.**",
				Computed:  true,
				Sensitive: true,
			},
			"credential_reset_token_ttl": schema.Int64Attribute{
				MarkdownDescription: "Time-to-live for the credential reset token in seconds. Defaults to 3600 (1 hour).",
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(3600),
			},
		},
	}
}

// Configure adds the provider configured client to the resource
func (r *personResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create creates the resource and sets the initial Terraform state
func (r *personResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan personResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Validate mutually exclusive options
	hasPassword := !plan.Password.IsNull() && !plan.Password.IsUnknown()
	generateToken := plan.GenerateCredentialResetToken.ValueBool()

	if hasPassword && generateToken {
		resp.Diagnostics.AddError(
			"Conflicting Configuration",
			"Cannot specify both 'password' and 'generate_credential_reset_token'. Choose one authentication setup method.",
		)
		return
	}

	tflog.Debug(ctx, "Creating person", map[string]any{
		"id": plan.ID.ValueString(),
	})

	// Create the person account
	person, err := r.client.CreatePerson(ctx, plan.ID.ValueString(), plan.DisplayName.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating Person",
			"Could not create person: "+err.Error(),
		)
		return
	}

	// Set password if provided
	if hasPassword {
		tflog.Debug(ctx, "Setting initial password for person")
		if err := r.client.SetPersonPassword(ctx, person.ID, plan.Password.ValueString()); err != nil {
			resp.Diagnostics.AddError(
				"Error Setting Password",
				"Person was created but password could not be set: "+err.Error(),
			)
			return
		}
	}

	// Generate credential reset token if requested
	if generateToken {
		tflog.Debug(ctx, "Generating credential reset token for person")
		ttl := int(plan.CredentialResetTokenTTL.ValueInt64())
		token, err := r.client.CreatePersonCredentialResetToken(ctx, person.ID, &ttl)
		if err != nil {
			resp.Diagnostics.AddError(
				"Error Generating Credential Reset Token",
				"Person was created but credential reset token could not be generated: "+err.Error(),
			)
			return
		}
		plan.CredentialResetToken = types.StringValue(token)
	}

	// PATCH any optional attrs the user declared (mail, legalname).
	// Legalname is only included when explicitly set in config —
	// matches the "don't touch what tofu doesn't declare" rule so
	// out-of-band legalname edits aren't reverted.
	var mailAddrs []string
	if !plan.Mail.IsNull() && !plan.Mail.IsUnknown() {
		resp.Diagnostics.Append(plan.Mail.ElementsAs(ctx, &mailAddrs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	updateOpts := client.UpdatePersonOpts{
		Mail: mailAddrs,
	}
	if !plan.Legalname.IsNull() && !plan.Legalname.IsUnknown() {
		v := plan.Legalname.ValueString()
		updateOpts.Legalname = &v
	}
	if len(mailAddrs) > 0 || updateOpts.Legalname != nil {
		if err := r.client.UpdatePerson(ctx, person.ID, updateOpts); err != nil {
			resp.Diagnostics.AddError(
				"Error Updating Person",
				"Person was created but its attributes could not be set: "+err.Error(),
			)
			return
		}
	}

	// Enable POSIX if requested. Pass the explicit gidnumber and/or
	// loginshell through if the user pinned them; otherwise let
	// Kanidm pick defaults.
	if plan.Posix.ValueBool() {
		var gid *int64
		if !plan.GidNumber.IsNull() && !plan.GidNumber.IsUnknown() {
			v := plan.GidNumber.ValueInt64()
			gid = &v
		}
		var shell *string
		if !plan.Loginshell.IsNull() && !plan.Loginshell.IsUnknown() {
			s := plan.Loginshell.ValueString()
			shell = &s
		}
		tflog.Debug(ctx, "Enabling POSIX on person", map[string]any{"id": person.ID})
		if err := r.client.EnablePersonPosix(ctx, person.ID, gid, shell); err != nil {
			resp.Diagnostics.AddError(
				"Error Enabling POSIX",
				"Person was created but POSIX could not be enabled: "+err.Error(),
			)
			return
		}
	}

	// Read back the person to get the current state
	createdPerson, err := r.client.GetPerson(ctx, person.ID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Person",
			"Person was created but could not be read back: "+err.Error(),
		)
		return
	}

	// Map response to state
	plan.ID = types.StringValue(createdPerson.ID)
	plan.DisplayName = types.StringValue(createdPerson.DisplayName)

	if len(createdPerson.Mail) > 0 {
		mailList, diags := types.ListValueFrom(ctx, types.StringType, createdPerson.Mail)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.Mail = mailList
	}

	// Legalname is Optional+Computed: state must hold whatever the
	// server has (so future refreshes don't fight out-of-band edits).
	plan.Legalname = types.StringValue(createdPerson.Legalname)

	// POSIX state. When posix isn't enabled, gidnumber/loginshell are
	// Null so the UseStateForUnknown planmodifier on a future plan
	// that doesn't declare them resolves to Null too (no spurious
	// diff). When posix is enabled, state mirrors server.
	plan.Posix = types.BoolValue(createdPerson.Posix)
	if createdPerson.Posix {
		plan.GidNumber = types.Int64Value(createdPerson.GidNumber)
		plan.Loginshell = types.StringValue(createdPerson.Loginshell)
	} else {
		plan.GidNumber = types.Int64Null()
		plan.Loginshell = types.StringNull()
	}

	// Password is write-only, keep the planned value but don't try to read it back

	// Ensure credential_reset_token fields are properly set with defaults if not already set
	if plan.GenerateCredentialResetToken.IsNull() || plan.GenerateCredentialResetToken.IsUnknown() {
		plan.GenerateCredentialResetToken = types.BoolValue(false)
	}
	if plan.CredentialResetTokenTTL.IsNull() || plan.CredentialResetTokenTTL.IsUnknown() {
		plan.CredentialResetTokenTTL = types.Int64Value(3600)
	}
	// If credential_reset_token wasn't generated, ensure it's null not unknown
	if plan.CredentialResetToken.IsUnknown() {
		plan.CredentialResetToken = types.StringNull()
	}

	tflog.Debug(ctx, "Person created successfully", map[string]any{
		"id": plan.ID.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read refreshes the Terraform state with the latest data
func (r *personResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state personResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Reading person", map[string]any{
		"id": state.ID.ValueString(),
	})

	// Get current person from API
	person, err := r.client.GetPerson(ctx, state.ID.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			tflog.Warn(ctx, "Person not found, removing from state", map[string]any{
				"id": state.ID.ValueString(),
			})
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error Reading Person",
			"Could not read person: "+err.Error(),
		)
		return
	}

	// Update state with current values
	state.ID = types.StringValue(person.ID)
	state.DisplayName = types.StringValue(person.DisplayName)

	if len(person.Mail) > 0 {
		mailList, diags := types.ListValueFrom(ctx, types.StringType, person.Mail)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		state.Mail = mailList
	} else {
		state.Mail = types.ListNull(types.StringType)
	}

	// Mirror legalname unconditionally — UseStateForUnknown handles
	// the "config doesn't declare it" case at plan-time so this
	// doesn't introduce drift.
	state.Legalname = types.StringValue(person.Legalname)

	// Mirror POSIX state. Null gidnumber/loginshell when not POSIX
	// so subsequent plans that don't declare them don't get diff.
	state.Posix = types.BoolValue(person.Posix)
	if person.Posix {
		state.GidNumber = types.Int64Value(person.GidNumber)
		state.Loginshell = types.StringValue(person.Loginshell)
	} else {
		state.GidNumber = types.Int64Null()
		state.Loginshell = types.StringNull()
	}

	// Password is write-only and not readable from API, preserve existing state value
	// credential_reset_token fields should use defaults when not explicitly set
	if state.GenerateCredentialResetToken.IsNull() || state.GenerateCredentialResetToken.IsUnknown() {
		state.GenerateCredentialResetToken = types.BoolValue(false)
	}
	if state.CredentialResetTokenTTL.IsNull() || state.CredentialResetTokenTTL.IsUnknown() {
		state.CredentialResetTokenTTL = types.Int64Value(3600)
	}
	// credential_reset_token is only set during Create/Update when generated, otherwise null
	if state.CredentialResetToken.IsUnknown() {
		state.CredentialResetToken = types.StringNull()
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update updates the resource and sets the updated Terraform state
func (r *personResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state personResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Updating person", map[string]any{
		"id": plan.ID.ValueString(),
	})

	// Prepare mail addresses
	var mailAddrs []string
	if !plan.Mail.IsNull() && !plan.Mail.IsUnknown() {
		resp.Diagnostics.Append(plan.Mail.ElementsAs(ctx, &mailAddrs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// POSIX disable check first — fail fast with a clear diagnostic.
	wasPosix := state.Posix.ValueBool()
	wantPosix := plan.Posix.ValueBool()
	if wasPosix && !wantPosix {
		resp.Diagnostics.AddAttributeError(
			path.Root("posix"),
			"POSIX cannot be disabled",
			"Kanidm doesn't support removing the `posixaccount` class once it has been set. "+
				"To remove POSIX status, the person has to be destroyed and re-created.",
		)
		return
	}

	updateOpts := client.UpdatePersonOpts{
		DisplayName: plan.DisplayName.ValueString(),
		Mail:        mailAddrs,
	}
	// Only PATCH legalname / loginshell when the user has explicitly
	// changed them. Optional+Computed+UseStateForUnknown means plan
	// values mirror state when config doesn't declare; the Equal
	// check distinguishes "user changed it" from "state propagated".
	if !plan.Legalname.Equal(state.Legalname) {
		v := plan.Legalname.ValueString()
		updateOpts.Legalname = &v
	}
	if wasPosix && !plan.Loginshell.Equal(state.Loginshell) {
		s := plan.Loginshell.ValueString()
		updateOpts.Loginshell = &s
	}

	if err := r.client.UpdatePerson(ctx, plan.ID.ValueString(), updateOpts); err != nil {
		resp.Diagnostics.AddError(
			"Error Updating Person",
			"Could not update person: "+err.Error(),
		)
		return
	}

	// POSIX enable transition (null/false → true). Pass through an
	// explicit gidnumber and/or shell if the user pinned them.
	if !wasPosix && wantPosix {
		var gid *int64
		if !plan.GidNumber.IsNull() && !plan.GidNumber.IsUnknown() {
			v := plan.GidNumber.ValueInt64()
			gid = &v
		}
		var shell *string
		if !plan.Loginshell.IsNull() && !plan.Loginshell.IsUnknown() {
			s := plan.Loginshell.ValueString()
			shell = &s
		}
		tflog.Debug(ctx, "Enabling POSIX on person", map[string]any{"id": plan.ID.ValueString()})
		if err := r.client.EnablePersonPosix(ctx, plan.ID.ValueString(), gid, shell); err != nil {
			resp.Diagnostics.AddError(
				"Error Enabling POSIX",
				"Could not enable POSIX on person: "+err.Error(),
			)
			return
		}
	}

	// Update password if changed
	if !plan.Password.Equal(state.Password) && !plan.Password.IsNull() {
		tflog.Debug(ctx, "Updating password for person")
		if err := r.client.SetPersonPassword(ctx, plan.ID.ValueString(), plan.Password.ValueString()); err != nil {
			resp.Diagnostics.AddError(
				"Error Updating Password",
				"Person was updated but password could not be changed: "+err.Error(),
			)
			return
		}
	}

	// Generate new credential reset token if requested and changed
	if plan.GenerateCredentialResetToken.ValueBool() && !plan.GenerateCredentialResetToken.Equal(state.GenerateCredentialResetToken) {
		tflog.Debug(ctx, "Generating new credential reset token for person")
		ttl := int(plan.CredentialResetTokenTTL.ValueInt64())
		token, err := r.client.CreatePersonCredentialResetToken(ctx, plan.ID.ValueString(), &ttl)
		if err != nil {
			resp.Diagnostics.AddError(
				"Error Generating Credential Reset Token",
				"Person was updated but credential reset token could not be generated: "+err.Error(),
			)
			return
		}
		plan.CredentialResetToken = types.StringValue(token)
	}

	// Read back the updated person
	updatedPerson, err := r.client.GetPerson(ctx, plan.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading Person",
			"Person was updated but could not be read back: "+err.Error(),
		)
		return
	}

	// Update state
	plan.ID = types.StringValue(updatedPerson.ID)
	plan.DisplayName = types.StringValue(updatedPerson.DisplayName)

	if len(updatedPerson.Mail) > 0 {
		mailList, diags := types.ListValueFrom(ctx, types.StringType, updatedPerson.Mail)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.Mail = mailList
	} else {
		plan.Mail = types.ListNull(types.StringType)
	}
	plan.Legalname = types.StringValue(updatedPerson.Legalname)

	plan.Posix = types.BoolValue(updatedPerson.Posix)
	if updatedPerson.Posix {
		plan.GidNumber = types.Int64Value(updatedPerson.GidNumber)
		plan.Loginshell = types.StringValue(updatedPerson.Loginshell)
	} else {
		plan.GidNumber = types.Int64Null()
		plan.Loginshell = types.StringNull()
	}

	// Ensure credential_reset_token fields are properly set
	if plan.GenerateCredentialResetToken.IsNull() || plan.GenerateCredentialResetToken.IsUnknown() {
		plan.GenerateCredentialResetToken = types.BoolValue(false)
	}
	if plan.CredentialResetTokenTTL.IsNull() || plan.CredentialResetTokenTTL.IsUnknown() {
		plan.CredentialResetTokenTTL = types.Int64Value(3600)
	}
	// credential_reset_token is only set during Update when generated, otherwise null
	if plan.CredentialResetToken.IsUnknown() {
		plan.CredentialResetToken = types.StringNull()
	}

	tflog.Debug(ctx, "Person updated successfully", map[string]any{
		"id": plan.ID.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete deletes the resource and removes the Terraform state
func (r *personResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state personResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Deleting person", map[string]any{
		"id": state.ID.ValueString(),
	})

	// Delete the person
	if err := r.client.DeletePerson(ctx, state.ID.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			// Person already deleted, just remove from state
			tflog.Warn(ctx, "Person not found during delete, removing from state", map[string]any{
				"id": state.ID.ValueString(),
			})
			return
		}

		resp.Diagnostics.AddError(
			"Error Deleting Person",
			"Could not delete person: "+err.Error(),
		)
		return
	}

	tflog.Debug(ctx, "Person deleted successfully", map[string]any{
		"id": state.ID.ValueString(),
	})
}

// ImportState imports an existing person into Terraform state
func (r *personResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Use the ID (username) directly as the import identifier
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)

	tflog.Debug(ctx, "Imported person", map[string]any{
		"id": req.ID,
	})
}
