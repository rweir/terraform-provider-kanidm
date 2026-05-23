package provider

import (
	"context"
	"errors"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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
	_ resource.Resource                = (*oauth2BasicResource)(nil)
	_ resource.ResourceWithImportState = (*oauth2BasicResource)(nil)
)

func NewOAuth2BasicResource() resource.Resource {
	return &oauth2BasicResource{}
}

type oauth2BasicResource struct {
	client *client.Client
}

type oauth2BasicResourceModel struct {
	Name         types.String `tfsdk:"name"`
	DisplayName  types.String `tfsdk:"displayname"`
	Origin       types.String `tfsdk:"origin"`
	RedirectURIs             types.Set    `tfsdk:"redirect_uris"`
	ScopeMaps                types.Set    `tfsdk:"scope_map"`
	ClientSecret             types.String `tfsdk:"client_secret"`
	PreferShortUsername      types.Bool   `tfsdk:"prefer_short_username"`
	AllowInsecureDisablePKCE types.Bool   `tfsdk:"allow_insecure_client_disable_pkce"`
	JWTLegacyCryptoEnable    types.Bool   `tfsdk:"jwt_legacy_crypto_enable"`
	ClaimMaps                types.Set    `tfsdk:"claim_map"`
}

type scopeMapModel struct {
	Group  types.String `tfsdk:"group"`
	Scopes types.Set    `tfsdk:"scopes"`
}

type claimMapModel struct {
	Name   types.String `tfsdk:"name"`
	Group  types.String `tfsdk:"group"`
	Values types.Set    `tfsdk:"values"`
}

func (r *oauth2BasicResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_oauth2_basic"
}

func (r *oauth2BasicResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: `Manages a Kanidm OAuth2 basic (confidential) client.

OAuth2 basic clients are used for server-side applications that can securely store a client secret.
The client secret is automatically generated on creation and can be used for OAuth2/OIDC authentication.

## Example Usage

` + "```hcl" + `
resource "kanidm_oauth2_basic" "grafana" {
  name        = "grafana"
  displayname = "Grafana"
  origin      = "https://grafana.example.com"

  redirect_uris = [
    "https://grafana.example.com/login/generic_oauth"
  ]

  scope_map {
    group  = "admins"
    scopes = ["openid", "profile", "email", "groups"]
  }

  scope_map {
    group  = "developers"
    scopes = ["openid", "profile", "email"]
  }
}

# Store the client secret in 1Password or another secret manager
output "grafana_client_secret" {
  value     = kanidm_oauth2_basic.grafana.client_secret
  sensitive = true
}
` + "```" + `

**Important:** The client secret is only available during creation and cannot be recovered later.
Store it securely immediately after creation. You can regenerate it using the Kanidm CLI if needed.`,

		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				MarkdownDescription: "Unique identifier for the OAuth2 client (client ID). Cannot be changed after creation.",
				Required:            true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"displayname": schema.StringAttribute{
				MarkdownDescription: "Display name of the OAuth2 client.",
				Required:            true,
			},
			"origin": schema.StringAttribute{
				MarkdownDescription: "Origin URL where the OAuth2 client application is hosted (e.g., https://grafana.example.com).",
				Required:            true,
			},
			"redirect_uris": schema.SetAttribute{
				MarkdownDescription: "Set of allowed redirect URIs for OAuth2 callbacks. " +
					"Order is not significant — Kanidm stores these as a multi-valued attribute.",
				Optional:            true,
				ElementType:         types.StringType,
			},
			"client_secret": schema.StringAttribute{
				MarkdownDescription: "Client secret for the OAuth2 basic client. **Only available during creation.** " +
					"Store this secret securely as it cannot be retrieved later.",
				Computed:  true,
				Sensitive: true,
			},
			"prefer_short_username": schema.BoolAttribute{
				MarkdownDescription: "If true, Kanidm emits the bare username (`name`) in the `preferred_username` " +
					"claim instead of the full SPN (`name@domain`). Useful for OIDC clients that treat " +
					"`preferred_username` as a single token rather than a user@host pair. " +
					"Maps to Kanidm's `oauth2_prefer_short_username` attribute.",
				Optional: true,
			},
			"allow_insecure_client_disable_pkce": schema.BoolAttribute{
				MarkdownDescription: "If true, this confidential client may complete the OAuth2 flow without sending " +
					"a `code_challenge` (PKCE). Useful only for confidential clients that don't support " +
					"PKCE (older Forgejo, Netbox, OpenGist). Leave unset for everything else. " +
					"Maps to Kanidm's `oauth2_allow_insecure_client_disable_pkce` attribute.",
				Optional: true,
			},
			"jwt_legacy_crypto_enable": schema.BoolAttribute{
				MarkdownDescription: "If true, Kanidm signs this client's JWTs with RS256 in addition to the " +
					"default ES256. Useful for OIDC libraries that can't speak ES256 (e.g. Netbox's " +
					"python-social-auth). Leave unset for everything else. " +
					"Maps to Kanidm's `oauth2_jwt_legacy_crypto_enable` attribute.",
				Optional: true,
			},
		},
		Blocks: map[string]schema.Block{
			"scope_map": schema.SetNestedBlock{
				MarkdownDescription: "Scope mappings that define which OAuth2 scopes are granted to members of specific groups. " +
					"Each scope_map block links a Kanidm group to a set of OAuth2 scopes.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"group": schema.StringAttribute{
							MarkdownDescription: "Name of the Kanidm group to map scopes to.",
							Required:            true,
						},
						"scopes": schema.SetAttribute{
							MarkdownDescription: "Set of OAuth2 scopes to grant to group members (e.g., openid, profile, email, groups). " +
								"Order is not significant — kanidm normalizes the set on storage.",
							Required:    true,
							ElementType: types.StringType,
						},
					},
				},
			},
			"claim_map": schema.SetNestedBlock{
				MarkdownDescription: "Claim mappings emit arbitrary string values in OIDC claims based on group " +
					"membership. Each block is keyed by the (name, group) tuple: when a user in `group` " +
					"authenticates against this client, `values` are joined into the `name` claim. " +
					"Used for role-like claims (Grafana `grafana_role`, Netbox `roles`, Otterwiki " +
					"`wiki_group`, …). Maps to Kanidm's `oauth2_rs_claim_map` attribute.",
				NestedObject: schema.NestedBlockObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							MarkdownDescription: "Name of the OIDC claim to emit (e.g. `grafana_role`).",
							Required:            true,
						},
						"group": schema.StringAttribute{
							MarkdownDescription: "Kanidm group whose members get this claim entry.",
							Required:            true,
						},
						"values": schema.SetAttribute{
							MarkdownDescription: "Values emitted in the claim for members of `group`. " +
								"Order is not significant — kanidm may reorder these.",
							Required:    true,
							ElementType: types.StringType,
						},
					},
				},
			},
		},
	}
}

func (r *oauth2BasicResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *oauth2BasicResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan oauth2BasicResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Creating OAuth2 basic client", map[string]any{
		"name": plan.Name.ValueString(),
	})

	// Create the OAuth2 basic client (this generates the client secret)
	oauth2Client, err := r.client.CreateOAuth2BasicClient(
		ctx,
		plan.Name.ValueString(),
		plan.DisplayName.ValueString(),
		plan.Origin.ValueString(),
	)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Creating OAuth2 Basic Client",
			"Could not create OAuth2 basic client: "+err.Error(),
		)
		return
	}

	// Update origin and redirect URIs after creation
	var redirectURIs []string
	if !plan.RedirectURIs.IsNull() && !plan.RedirectURIs.IsUnknown() {
		resp.Diagnostics.Append(plan.RedirectURIs.ElementsAs(ctx, &redirectURIs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	tflog.Debug(ctx, "Setting displayname, origin and redirect URIs for OAuth2 client", map[string]any{
		"displayname":    plan.DisplayName.ValueString(),
		"origin":         plan.Origin.ValueString(),
		"redirect_count": len(redirectURIs),
	})

	updateOpts := client.UpdateOAuth2ClientOpts{
		DisplayName:  plan.DisplayName.ValueString(),
		Origin:       plan.Origin.ValueString(),
		RedirectURIs: redirectURIs,
	}
	if !plan.PreferShortUsername.IsNull() && !plan.PreferShortUsername.IsUnknown() {
		v := plan.PreferShortUsername.ValueBool()
		updateOpts.PreferShortUsername = &v
	}
	if !plan.AllowInsecureDisablePKCE.IsNull() && !plan.AllowInsecureDisablePKCE.IsUnknown() {
		v := plan.AllowInsecureDisablePKCE.ValueBool()
		updateOpts.AllowInsecureDisablePKCE = &v
	}
	if !plan.JWTLegacyCryptoEnable.IsNull() && !plan.JWTLegacyCryptoEnable.IsUnknown() {
		v := plan.JWTLegacyCryptoEnable.ValueBool()
		updateOpts.JWTLegacyCryptoEnable = &v
	}

	if err := r.client.UpdateOAuth2Client(ctx, oauth2Client.Name, updateOpts); err != nil {
		resp.Diagnostics.AddError(
			"Error Setting OAuth2 Configuration",
			"OAuth2 client was created but configuration could not be set: "+err.Error(),
		)
		return
	}

	// Configure scope maps if provided
	if !plan.ScopeMaps.IsNull() && !plan.ScopeMaps.IsUnknown() {
		var scopeMaps []scopeMapModel
		resp.Diagnostics.Append(plan.ScopeMaps.ElementsAs(ctx, &scopeMaps, false)...)
		if resp.Diagnostics.HasError() {
			return
		}

		for _, scopeMap := range scopeMaps {
			var scopes []string
			resp.Diagnostics.Append(scopeMap.Scopes.ElementsAs(ctx, &scopes, false)...)
			if resp.Diagnostics.HasError() {
				return
			}

			tflog.Debug(ctx, "Setting scope map for OAuth2 client", map[string]any{
				"group":  scopeMap.Group.ValueString(),
				"scopes": scopes,
			})

			if err := r.client.SetOAuth2ScopeMap(ctx, oauth2Client.Name, scopeMap.Group.ValueString(), scopes); err != nil {
				resp.Diagnostics.AddError(
					"Error Setting Scope Map",
					"OAuth2 client was created but scope map could not be configured: "+err.Error(),
				)
				return
			}
		}
	}

	// Configure claim maps if provided. Each claim_map entry is its
	// own sub-resource at /v1/oauth2/<rs>/_claimmap/<name>/<group>.
	if !plan.ClaimMaps.IsNull() && !plan.ClaimMaps.IsUnknown() {
		var claimMaps []claimMapModel
		resp.Diagnostics.Append(plan.ClaimMaps.ElementsAs(ctx, &claimMaps, false)...)
		if resp.Diagnostics.HasError() {
			return
		}

		for _, cm := range claimMaps {
			var values []string
			resp.Diagnostics.Append(cm.Values.ElementsAs(ctx, &values, false)...)
			if resp.Diagnostics.HasError() {
				return
			}

			tflog.Debug(ctx, "Setting claim map for OAuth2 client", map[string]any{
				"name":   cm.Name.ValueString(),
				"group":  cm.Group.ValueString(),
				"values": values,
			})

			if err := r.client.SetOAuth2ClaimMap(ctx, oauth2Client.Name, cm.Name.ValueString(), cm.Group.ValueString(), values); err != nil {
				resp.Diagnostics.AddError(
					"Error Setting Claim Map",
					"OAuth2 client was created but claim map could not be configured: "+err.Error(),
				)
				return
			}
		}
	}

	// Read back the created OAuth2 client
	createdClient, err := r.client.GetOAuth2Client(ctx, oauth2Client.Name)
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading OAuth2 Client",
			"OAuth2 client was created but could not be read back: "+err.Error(),
		)
		return
	}

	// Map response to state
	plan.Name = types.StringValue(createdClient.Name)
	plan.DisplayName = types.StringValue(createdClient.DisplayName)
	plan.Origin = types.StringValue(createdClient.Origin)
	plan.ClientSecret = types.StringValue(oauth2Client.ClientSecret)

	if len(createdClient.RedirectURIs) > 0 {
		redirectURIsSet, diags := types.SetValueFrom(ctx, types.StringType, createdClient.RedirectURIs)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.RedirectURIs = redirectURIsSet
	} else {
		plan.RedirectURIs = types.SetNull(types.StringType)
	}

	// Preserve null on prefer_short_username: if the user didn't set
	// the attribute and the server doesn't have it set either, leave
	// state Null rather than turning it into an explicit false.
	if !plan.PreferShortUsername.IsNull() || createdClient.PreferShortUsernameSet {
		plan.PreferShortUsername = types.BoolValue(createdClient.PreferShortUsername)
	}
	// Same null-preservation for allow_insecure_client_disable_pkce.
	if !plan.AllowInsecureDisablePKCE.IsNull() || createdClient.AllowInsecureDisablePKCESet {
		plan.AllowInsecureDisablePKCE = types.BoolValue(createdClient.AllowInsecureDisablePKCE)
	}
	// Same null-preservation for jwt_legacy_crypto_enable.
	if !plan.JWTLegacyCryptoEnable.IsNull() || createdClient.JWTLegacyCryptoEnableSet {
		plan.JWTLegacyCryptoEnable = types.BoolValue(createdClient.JWTLegacyCryptoEnable)
	}

	// Keep the scope maps from the plan (can't read them back from API in current form)
	// In a future enhancement, we could parse the scope maps from the API response

	tflog.Debug(ctx, "OAuth2 basic client created successfully", map[string]any{
		"name": plan.Name.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *oauth2BasicResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state oauth2BasicResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Reading OAuth2 basic client", map[string]any{
		"name": state.Name.ValueString(),
	})

	// Get current OAuth2 client from API
	oauth2Client, err := r.client.GetOAuth2Client(ctx, state.Name.ValueString())
	if err != nil {
		if errors.Is(err, client.ErrNotFound) {
			tflog.Warn(ctx, "OAuth2 basic client not found, removing from state", map[string]any{
				"name": state.Name.ValueString(),
			})
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError(
			"Error Reading OAuth2 Basic Client",
			"Could not read OAuth2 basic client: "+err.Error(),
		)
		return
	}

	// Verify this is a basic (confidential) client
	if oauth2Client.IsPublic {
		resp.Diagnostics.AddError(
			"Invalid Client Type",
			"Expected OAuth2 basic (confidential) client but found public client. "+
				"This resource manages basic clients only.",
		)
		return
	}

	// Update state with current values
	state.Name = types.StringValue(oauth2Client.Name)
	state.DisplayName = types.StringValue(oauth2Client.DisplayName)
	state.Origin = types.StringValue(oauth2Client.Origin)

	// Mirror prefer_short_username when the server has it set; if the
	// server doesn't have it and state already had it Null, leave it
	// Null (avoids introducing drift on refresh of a config that
	// doesn't manage this attribute).
	if !state.PreferShortUsername.IsNull() || oauth2Client.PreferShortUsernameSet {
		state.PreferShortUsername = types.BoolValue(oauth2Client.PreferShortUsername)
	}
	// Same null-preservation for allow_insecure_client_disable_pkce.
	if !state.AllowInsecureDisablePKCE.IsNull() || oauth2Client.AllowInsecureDisablePKCESet {
		state.AllowInsecureDisablePKCE = types.BoolValue(oauth2Client.AllowInsecureDisablePKCE)
	}
	// Same null-preservation for jwt_legacy_crypto_enable.
	if !state.JWTLegacyCryptoEnable.IsNull() || oauth2Client.JWTLegacyCryptoEnableSet {
		state.JWTLegacyCryptoEnable = types.BoolValue(oauth2Client.JWTLegacyCryptoEnable)
	}

	if len(oauth2Client.RedirectURIs) > 0 {
		redirectURIsSet, diags := types.SetValueFrom(ctx, types.StringType, oauth2Client.RedirectURIs)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		state.RedirectURIs = redirectURIsSet
	} else {
		state.RedirectURIs = types.SetNull(types.StringType)
	}

	// Retrieve client secret if not already in state (e.g., after import)
	if state.ClientSecret.IsNull() || state.ClientSecret.ValueString() == "" {
		tflog.Debug(ctx, "Client secret not in state, retrieving from API", map[string]any{
			"name": state.Name.ValueString(),
		})
		secret, err := r.client.GetOAuth2BasicSecret(ctx, state.Name.ValueString())
		if err != nil {
			tflog.Warn(ctx, "Could not retrieve client secret", map[string]any{
				"name":  state.Name.ValueString(),
				"error": err.Error(),
			})
			// Don't fail the read, just leave secret empty
		} else {
			state.ClientSecret = types.StringValue(secret)
			tflog.Debug(ctx, "Retrieved client secret successfully", map[string]any{
				"name": state.Name.ValueString(),
			})
		}
	}

	// Populate scope_maps and claim_maps from the parsed entry. If the
	// server has no entries, set state Null (matches "block not
	// declared in config" semantics — avoids spurious empty-vs-null
	// drift).
	scopeMapAttrTypes := map[string]attr.Type{
		"group":  types.StringType,
		"scopes": types.SetType{ElemType: types.StringType},
	}
	if len(oauth2Client.ScopeMaps) > 0 {
		scopeMapModels := make([]scopeMapModel, 0, len(oauth2Client.ScopeMaps))
		for group, scopes := range oauth2Client.ScopeMaps {
			scopesSet, diags := types.SetValueFrom(ctx, types.StringType, scopes)
			resp.Diagnostics.Append(diags...)
			scopeMapModels = append(scopeMapModels, scopeMapModel{
				Group:  types.StringValue(group),
				Scopes: scopesSet,
			})
		}
		smSet, diags := types.SetValueFrom(ctx, types.ObjectType{AttrTypes: scopeMapAttrTypes}, scopeMapModels)
		resp.Diagnostics.Append(diags...)
		state.ScopeMaps = smSet
	} else {
		state.ScopeMaps = types.SetNull(types.ObjectType{AttrTypes: scopeMapAttrTypes})
	}

	claimMapAttrTypes := map[string]attr.Type{
		"name":   types.StringType,
		"group":  types.StringType,
		"values": types.SetType{ElemType: types.StringType},
	}
	if len(oauth2Client.ClaimMaps) > 0 {
		claimMapModels := make([]claimMapModel, 0, len(oauth2Client.ClaimMaps))
		for _, cm := range oauth2Client.ClaimMaps {
			valuesSet, diags := types.SetValueFrom(ctx, types.StringType, cm.Values)
			resp.Diagnostics.Append(diags...)
			claimMapModels = append(claimMapModels, claimMapModel{
				Name:   types.StringValue(cm.Name),
				Group:  types.StringValue(cm.Group),
				Values: valuesSet,
			})
		}
		cmSet, diags := types.SetValueFrom(ctx, types.ObjectType{AttrTypes: claimMapAttrTypes}, claimMapModels)
		resp.Diagnostics.Append(diags...)
		state.ClaimMaps = cmSet
	} else {
		state.ClaimMaps = types.SetNull(types.ObjectType{AttrTypes: claimMapAttrTypes})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *oauth2BasicResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state oauth2BasicResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Updating OAuth2 basic client", map[string]any{
		"name": plan.Name.ValueString(),
	})

	// Prepare redirect URIs
	var redirectURIs []string
	if !plan.RedirectURIs.IsNull() && !plan.RedirectURIs.IsUnknown() {
		resp.Diagnostics.Append(plan.RedirectURIs.ElementsAs(ctx, &redirectURIs, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	// Update OAuth2 client (displayname, origin, redirect URIs, plus
	// any out-of-band bools the user set in the plan).
	updateOpts := client.UpdateOAuth2ClientOpts{
		DisplayName:  plan.DisplayName.ValueString(),
		Origin:       plan.Origin.ValueString(),
		RedirectURIs: redirectURIs,
	}
	if !plan.PreferShortUsername.IsNull() && !plan.PreferShortUsername.IsUnknown() {
		v := plan.PreferShortUsername.ValueBool()
		updateOpts.PreferShortUsername = &v
	} else if !state.PreferShortUsername.IsNull() {
		// The user has removed the attribute from the plan but it
		// was set in state — clear it server-side.
		v := false
		updateOpts.PreferShortUsername = &v
	}
	if !plan.AllowInsecureDisablePKCE.IsNull() && !plan.AllowInsecureDisablePKCE.IsUnknown() {
		v := plan.AllowInsecureDisablePKCE.ValueBool()
		updateOpts.AllowInsecureDisablePKCE = &v
	} else if !state.AllowInsecureDisablePKCE.IsNull() {
		v := false
		updateOpts.AllowInsecureDisablePKCE = &v
	}
	if !plan.JWTLegacyCryptoEnable.IsNull() && !plan.JWTLegacyCryptoEnable.IsUnknown() {
		v := plan.JWTLegacyCryptoEnable.ValueBool()
		updateOpts.JWTLegacyCryptoEnable = &v
	} else if !state.JWTLegacyCryptoEnable.IsNull() {
		v := false
		updateOpts.JWTLegacyCryptoEnable = &v
	}

	if err := r.client.UpdateOAuth2Client(ctx, plan.Name.ValueString(), updateOpts); err != nil {
		resp.Diagnostics.AddError(
			"Error Updating OAuth2 Basic Client",
			"Could not update OAuth2 basic client: "+err.Error(),
		)
		return
	}

	// Handle scope map changes
	// Get old and new scope maps
	var oldScopeMaps, newScopeMaps []scopeMapModel
	resp.Diagnostics.Append(state.ScopeMaps.ElementsAs(ctx, &oldScopeMaps, false)...)
	resp.Diagnostics.Append(plan.ScopeMaps.ElementsAs(ctx, &newScopeMaps, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Build maps for easier comparison
	oldScopeMapsByGroup := make(map[string][]string)
	for _, sm := range oldScopeMaps {
		var scopes []string
		resp.Diagnostics.Append(sm.Scopes.ElementsAs(ctx, &scopes, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		oldScopeMapsByGroup[sm.Group.ValueString()] = scopes
	}

	newScopeMapsByGroup := make(map[string][]string)
	for _, sm := range newScopeMaps {
		var scopes []string
		resp.Diagnostics.Append(sm.Scopes.ElementsAs(ctx, &scopes, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		newScopeMapsByGroup[sm.Group.ValueString()] = scopes
	}

	// Delete scope maps that are no longer present
	for group := range oldScopeMapsByGroup {
		if _, exists := newScopeMapsByGroup[group]; !exists {
			tflog.Debug(ctx, "Deleting scope map", map[string]any{
				"group": group,
			})
			if err := r.client.DeleteOAuth2ScopeMap(ctx, plan.Name.ValueString(), group); err != nil {
				resp.Diagnostics.AddError(
					"Error Deleting Scope Map",
					"Could not delete scope map: "+err.Error(),
				)
				return
			}
		}
	}

	// Add or update scope maps
	for group, scopes := range newScopeMapsByGroup {
		tflog.Debug(ctx, "Setting scope map", map[string]any{
			"group":  group,
			"scopes": scopes,
		})
		if err := r.client.SetOAuth2ScopeMap(ctx, plan.Name.ValueString(), group, scopes); err != nil {
			resp.Diagnostics.AddError(
				"Error Setting Scope Map",
				"Could not set scope map: "+err.Error(),
			)
			return
		}
	}

	// Handle claim_map changes. Each entry is keyed by the (name,
	// group) tuple; we diff state vs plan and delete entries that are
	// gone, then upsert the rest. Mirrors the scope_map diff above.
	var oldClaimMaps, newClaimMaps []claimMapModel
	resp.Diagnostics.Append(state.ClaimMaps.ElementsAs(ctx, &oldClaimMaps, false)...)
	resp.Diagnostics.Append(plan.ClaimMaps.ElementsAs(ctx, &newClaimMaps, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	type claimKey struct{ Name, Group string }
	oldClaimsByKey := make(map[claimKey][]string)
	for _, cm := range oldClaimMaps {
		var values []string
		resp.Diagnostics.Append(cm.Values.ElementsAs(ctx, &values, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		oldClaimsByKey[claimKey{cm.Name.ValueString(), cm.Group.ValueString()}] = values
	}

	newClaimsByKey := make(map[claimKey][]string)
	for _, cm := range newClaimMaps {
		var values []string
		resp.Diagnostics.Append(cm.Values.ElementsAs(ctx, &values, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		newClaimsByKey[claimKey{cm.Name.ValueString(), cm.Group.ValueString()}] = values
	}

	for key := range oldClaimsByKey {
		if _, exists := newClaimsByKey[key]; !exists {
			tflog.Debug(ctx, "Deleting claim map", map[string]any{
				"name":  key.Name,
				"group": key.Group,
			})
			if err := r.client.DeleteOAuth2ClaimMap(ctx, plan.Name.ValueString(), key.Name, key.Group); err != nil {
				resp.Diagnostics.AddError(
					"Error Deleting Claim Map",
					"Could not delete claim map: "+err.Error(),
				)
				return
			}
		}
	}

	for key, values := range newClaimsByKey {
		tflog.Debug(ctx, "Setting claim map", map[string]any{
			"name":   key.Name,
			"group":  key.Group,
			"values": values,
		})
		if err := r.client.SetOAuth2ClaimMap(ctx, plan.Name.ValueString(), key.Name, key.Group, values); err != nil {
			resp.Diagnostics.AddError(
				"Error Setting Claim Map",
				"Could not set claim map: "+err.Error(),
			)
			return
		}
	}

	// Read back the updated OAuth2 client
	updatedClient, err := r.client.GetOAuth2Client(ctx, plan.Name.ValueString())
	if err != nil {
		resp.Diagnostics.AddError(
			"Error Reading OAuth2 Client",
			"OAuth2 client was updated but could not be read back: "+err.Error(),
		)
		return
	}

	// Update state
	plan.Name = types.StringValue(updatedClient.Name)
	plan.DisplayName = types.StringValue(updatedClient.DisplayName)
	plan.Origin = types.StringValue(updatedClient.Origin)

	if len(updatedClient.RedirectURIs) > 0 {
		redirectURIsSet, diags := types.SetValueFrom(ctx, types.StringType, updatedClient.RedirectURIs)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		plan.RedirectURIs = redirectURIsSet
	} else {
		plan.RedirectURIs = types.SetNull(types.StringType)
	}

	// Same null-preservation logic as Create: keep Null when neither
	// plan nor server has the attribute set.
	if !plan.PreferShortUsername.IsNull() || updatedClient.PreferShortUsernameSet {
		plan.PreferShortUsername = types.BoolValue(updatedClient.PreferShortUsername)
	}
	// Same for allow_insecure_client_disable_pkce.
	if !plan.AllowInsecureDisablePKCE.IsNull() || updatedClient.AllowInsecureDisablePKCESet {
		plan.AllowInsecureDisablePKCE = types.BoolValue(updatedClient.AllowInsecureDisablePKCE)
	}
	// Same for jwt_legacy_crypto_enable.
	if !plan.JWTLegacyCryptoEnable.IsNull() || updatedClient.JWTLegacyCryptoEnableSet {
		plan.JWTLegacyCryptoEnable = types.BoolValue(updatedClient.JWTLegacyCryptoEnable)
	}

	// Preserve client secret from state (cannot be read back from API)
	plan.ClientSecret = state.ClientSecret

	tflog.Debug(ctx, "OAuth2 basic client updated successfully", map[string]any{
		"name": plan.Name.ValueString(),
	})

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *oauth2BasicResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state oauth2BasicResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Debug(ctx, "Deleting OAuth2 basic client", map[string]any{
		"name": state.Name.ValueString(),
	})

	// Delete the OAuth2 client
	if err := r.client.DeleteOAuth2Client(ctx, state.Name.ValueString()); err != nil {
		if errors.Is(err, client.ErrNotFound) {
			tflog.Warn(ctx, "OAuth2 basic client not found during delete, removing from state", map[string]any{
				"name": state.Name.ValueString(),
			})
			return
		}

		resp.Diagnostics.AddError(
			"Error Deleting OAuth2 Basic Client",
			"Could not delete OAuth2 basic client: "+err.Error(),
		)
		return
	}

	tflog.Debug(ctx, "OAuth2 basic client deleted successfully", map[string]any{
		"name": state.Name.ValueString(),
	})
}

func (r *oauth2BasicResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	// Use the name directly as the import identifier
	resource.ImportStatePassthroughID(ctx, path.Root("name"), req, resp)

	tflog.Debug(ctx, "Imported OAuth2 basic client", map[string]any{
		"name": req.ID,
	})

	// Add a warning about the client secret
	resp.Diagnostics.AddWarning(
		"Client Secret Not Available",
		"The client secret for this OAuth2 basic client is not available after import. "+
			"If you need the secret, you must regenerate it manually using the Kanidm CLI (kanidm system oauth2 basic_secret_read).",
	)
}
