package client

import (
	"context"
	"fmt"
	"strings"
)

// OAuth2Client represents a Kanidm OAuth2 resource server
type OAuth2Client struct {
	Name         string
	DisplayName  string
	Origin       string
	RedirectURIs []string
	ScopeMaps    map[string][]string
	ClaimMaps    []ClaimMapEntry
	ClientID     string // Computed
	ClientSecret string // Only for basic/confidential clients, populated on creation
	IsPublic     bool

	// PreferShortUsername mirrors Kanidm's
	// `oauth2_prefer_short_username` attribute. The bool itself only
	// matters when PreferShortUsernameSet is true — see GetOAuth2Client.
	PreferShortUsername    bool
	PreferShortUsernameSet bool

	// AllowInsecureDisablePKCE mirrors Kanidm's
	// `oauth2_allow_insecure_client_disable_pkce`. Same Set-flag
	// convention as PreferShortUsername.
	AllowInsecureDisablePKCE    bool
	AllowInsecureDisablePKCESet bool

	// JWTLegacyCryptoEnable mirrors Kanidm's
	// `oauth2_jwt_legacy_crypto_enable`. Same Set-flag convention.
	JWTLegacyCryptoEnable    bool
	JWTLegacyCryptoEnableSet bool
}

// ClaimMapEntry is one (claim_name, group, values) tuple from
// `oauth2_rs_claim_map`. The on-the-wire format from Kanidm is
//
//	<name>:<group>@<domain>:;:"v1,v2,..."
//
// — group SPNs come back with the kanidm domain appended.
type ClaimMapEntry struct {
	Name   string
	Group  string
	Values []string
}

// parseClaimMapEntry parses one `oauth2_rs_claim_map` line.
func parseClaimMapEntry(s string) (ClaimMapEntry, bool) {
	sepIdx := strings.Index(s, ":;:")
	if sepIdx == -1 {
		return ClaimMapEntry{}, false
	}
	prefix := s[:sepIdx]
	suffix := strings.TrimSpace(s[sepIdx+len(":;:"):])

	nameSep := strings.Index(prefix, ":")
	if nameSep == -1 {
		return ClaimMapEntry{}, false
	}
	name := prefix[:nameSep]
	group := stripSPNDomain(prefix[nameSep+1:])

	suffix = strings.TrimPrefix(strings.TrimSuffix(suffix, `"`), `"`)
	var values []string
	if suffix != "" {
		values = strings.Split(suffix, ",")
	}

	return ClaimMapEntry{Name: name, Group: group, Values: values}, true
}

// parseScopeMapEntry parses one `oauth2_rs_scope_map` line. Format:
//
//	<group>@<domain>: {"scope1", "scope2", ...}
//
// Returns (group, scopes, ok).
func parseScopeMapEntry(s string) (string, []string, bool) {
	sepIdx := strings.Index(s, ": {")
	if sepIdx == -1 {
		return "", nil, false
	}
	group := stripSPNDomain(strings.TrimSpace(s[:sepIdx]))
	inner := strings.TrimSpace(s[sepIdx+len(": {"):])
	inner = strings.TrimSuffix(inner, "}")

	var scopes []string
	for _, part := range strings.Split(inner, ",") {
		part = strings.TrimSpace(part)
		part = strings.TrimPrefix(strings.TrimSuffix(part, `"`), `"`)
		if part != "" {
			scopes = append(scopes, part)
		}
	}
	return group, scopes, true
}

// stripSPNDomain strips the @<domain> suffix from a kanidm SPN.
// Tofu configs use bare group names; the API returns fully qualified
// SPNs.
func stripSPNDomain(spn string) string {
	if at := strings.Index(spn, "@"); at != -1 {
		return spn[:at]
	}
	return spn
}

// CreateOAuth2BasicClient creates a new OAuth2 basic (confidential) client
func (c *Client) CreateOAuth2BasicClient(ctx context.Context, name, displayName, origin string) (*OAuth2Client, error) {
	req := NewCreateRequest(map[string]any{
		"name":                     []string{name},
		"displayname":              []string{displayName},
		"oauth2_rs_origin_landing": []string{origin},
	})

	resp, err := c.doRequest(ctx, "POST", "/v1/oauth2/_basic", req)
	if err != nil {
		return nil, fmt.Errorf("create oauth2 basic client: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// The create response doesn't include the client secret
	// We need to retrieve it using the show secret endpoint
	clientSecret, err := c.GetOAuth2BasicSecret(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("retrieve client secret: %w", err)
	}

	return &OAuth2Client{
		Name:         name,
		DisplayName:  displayName,
		Origin:       origin,
		ClientID:     name, // Client ID is typically the name
		ClientSecret: clientSecret,
		IsPublic:     false,
	}, nil
}

// CreateOAuth2PublicClient creates a new OAuth2 public client
func (c *Client) CreateOAuth2PublicClient(ctx context.Context, name, displayName, origin string) (*OAuth2Client, error) {
	req := NewCreateRequest(map[string]any{
		"name":                     []string{name},
		"displayname":              []string{displayName},
		"oauth2_rs_origin_landing": []string{origin},
	})

	resp, err := c.doRequest(ctx, "POST", "/v1/oauth2/_public", req)
	if err != nil {
		return nil, fmt.Errorf("create oauth2 public client: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return &OAuth2Client{
		Name:        name,
		DisplayName: displayName,
		Origin:      origin,
		ClientID:    name,
		IsPublic:    true,
	}, nil
}

// GetOAuth2Client retrieves an OAuth2 client by name
func (c *Client) GetOAuth2Client(ctx context.Context, name string) (*OAuth2Client, error) {
	resp, err := c.doRequest(ctx, "GET", "/v1/oauth2/"+name, nil)
	if err != nil {
		return nil, fmt.Errorf("get oauth2 client: %w", err)
	}

	var entry Entry
	if err := decodeResponse(resp, &entry); err != nil {
		return nil, err
	}

	// Determine basic vs public from the entry's class array. Older
	// versions of this provider keyed off `oauth2_rs_basic_secret`
	// being present, but newer Kanidm versions don't always include
	// that attribute in GET responses (it's hidden, and some auth
	// contexts omit hidden attrs entirely). The class array is the
	// canonical signal.
	classes := entry.GetStringSlice("class")
	isPublic := false
	for _, cls := range classes {
		if cls == "oauth2_resource_server_public" {
			isPublic = true
			break
		}
	}

	// Use 'name' attribute for the name (not oauth2_rs_name which is for internal use)
	clientName := entry.GetString("name")
	if clientName == "" {
		clientName = entry.GetString("oauth2_rs_name")
	}

	// Get origin (post-auth landing URL) and normalize by removing trailing
	// slash if present — Kanidm adds it but Terraform configs typically don't.
	// Kanidm calls this oauth2_rs_origin_landing. The multi-valued
	// oauth2_rs_origin holds the OAuth2 callback URLs (redirect_uris).
	origin := entry.GetString("oauth2_rs_origin_landing")
	if len(origin) > 0 && origin[len(origin)-1] == '/' {
		origin = origin[:len(origin)-1]
	}

	preferShort, preferShortSet := entry.GetBool("oauth2_prefer_short_username")
	disablePKCE, disablePKCESet := entry.GetBool("oauth2_allow_insecure_client_disable_pkce")
	jwtLegacy, jwtLegacySet := entry.GetBool("oauth2_jwt_legacy_crypto_enable")

	scopeMaps := make(map[string][]string)
	for _, line := range entry.GetStringSlice("oauth2_rs_scope_map") {
		if group, scopes, ok := parseScopeMapEntry(line); ok {
			scopeMaps[group] = scopes
		}
	}

	var claimMaps []ClaimMapEntry
	for _, line := range entry.GetStringSlice("oauth2_rs_claim_map") {
		if cm, ok := parseClaimMapEntry(line); ok {
			claimMaps = append(claimMaps, cm)
		}
	}

	return &OAuth2Client{
		Name:         clientName,
		DisplayName:  entry.GetString("displayname"),
		Origin:       origin,
		RedirectURIs: entry.GetStringSlice("oauth2_rs_origin"),
		ScopeMaps:    scopeMaps,
		ClaimMaps:    claimMaps,
		ClientID:     clientName,
		IsPublic:     isPublic,

		PreferShortUsername:    preferShort,
		PreferShortUsernameSet: preferShortSet,

		AllowInsecureDisablePKCE:    disablePKCE,
		AllowInsecureDisablePKCESet: disablePKCESet,

		JWTLegacyCryptoEnable:    jwtLegacy,
		JWTLegacyCryptoEnableSet: jwtLegacySet,
		// Note: Client secret is never returned in GET responses
	}, nil
}

// UpdateOAuth2ClientOpts is the set of attributes that can be PATCHed
// on an OAuth2 client. Fields whose Go zero value is "no change"
// (DisplayName=="", Origin=="", RedirectURIs==nil) follow that
// convention; for boolean attrs we use *bool so nil means "don't touch"
// and an explicit &true / &false sets the value.
type UpdateOAuth2ClientOpts struct {
	DisplayName              string
	Origin                   string
	RedirectURIs             []string
	PreferShortUsername      *bool
	AllowInsecureDisablePKCE *bool
	JWTLegacyCryptoEnable    *bool
}

// UpdateOAuth2Client PATCHes the named OAuth2 client. Only attributes
// present in opts are touched server-side, so out-of-band attributes
// the provider doesn't yet model are preserved.
func (c *Client) UpdateOAuth2Client(ctx context.Context, name string, opts UpdateOAuth2ClientOpts) error {
	attrs := make(map[string]any)

	if opts.DisplayName != "" {
		attrs["displayname"] = []string{opts.DisplayName}
	}

	if opts.Origin != "" {
		attrs["oauth2_rs_origin_landing"] = []string{opts.Origin}
	}

	if opts.RedirectURIs != nil {
		attrs["oauth2_rs_origin"] = opts.RedirectURIs
	}

	if opts.PreferShortUsername != nil {
		// Kanidm encodes single-valued boolean attrs as string arrays.
		val := "false"
		if *opts.PreferShortUsername {
			val = "true"
		}
		attrs["oauth2_prefer_short_username"] = []string{val}
	}

	if opts.AllowInsecureDisablePKCE != nil {
		// Kanidm clears this one with an empty array (rather than
		// `["false"]`) — see the kanidm CLI's `warning-enable-pkce`.
		if *opts.AllowInsecureDisablePKCE {
			attrs["oauth2_allow_insecure_client_disable_pkce"] = []string{"true"}
		} else {
			attrs["oauth2_allow_insecure_client_disable_pkce"] = []string{}
		}
	}

	if opts.JWTLegacyCryptoEnable != nil {
		// Symmetric with prefer_short_username: kanidm accepts
		// ["true"] / ["false"] on the same attribute.
		val := "false"
		if *opts.JWTLegacyCryptoEnable {
			val = "true"
		}
		attrs["oauth2_jwt_legacy_crypto_enable"] = []string{val}
	}

	req := NewUpdateRequest(attrs)

	resp, err := c.doRequest(ctx, "PATCH", "/v1/oauth2/"+name, req)
	if err != nil {
		return fmt.Errorf("update oauth2 client: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// DeleteOAuth2Client deletes an OAuth2 client
func (c *Client) DeleteOAuth2Client(ctx context.Context, name string) error {
	resp, err := c.doRequest(ctx, "DELETE", "/v1/oauth2/"+name, nil)
	if err != nil {
		return fmt.Errorf("delete oauth2 client: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// SetOAuth2ScopeMap sets the scope mapping for an OAuth2 client
func (c *Client) SetOAuth2ScopeMap(ctx context.Context, rsName, groupName string, scopes []string) error {
	// Send scopes array directly (not wrapped in an object)
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/v1/oauth2/%s/_scopemap/%s", rsName, groupName), scopes)
	if err != nil {
		return fmt.Errorf("set oauth2 scope map: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// DeleteOAuth2ScopeMap removes a scope mapping for an OAuth2 client
func (c *Client) DeleteOAuth2ScopeMap(ctx context.Context, rsName, groupName string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/v1/oauth2/%s/_scopemap/%s", rsName, groupName), nil)
	if err != nil {
		return fmt.Errorf("delete oauth2 scope map: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// SetOAuth2ClaimMap sets one claim_map entry on an OAuth2 client.
// Each entry is keyed by the (claim_name, group) tuple; values is
// the list of strings to emit when a user in `group` is granted
// `claim_name`. POSTing replaces any existing values for that tuple.
func (c *Client) SetOAuth2ClaimMap(ctx context.Context, rsName, claimName, groupName string, values []string) error {
	resp, err := c.doRequest(ctx, "POST",
		fmt.Sprintf("/v1/oauth2/%s/_claimmap/%s/%s", rsName, claimName, groupName), values)
	if err != nil {
		return fmt.Errorf("set oauth2 claim map: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// DeleteOAuth2ClaimMap removes one claim_map entry from an OAuth2 client.
func (c *Client) DeleteOAuth2ClaimMap(ctx context.Context, rsName, claimName, groupName string) error {
	resp, err := c.doRequest(ctx, "DELETE",
		fmt.Sprintf("/v1/oauth2/%s/_claimmap/%s/%s", rsName, claimName, groupName), nil)
	if err != nil {
		return fmt.Errorf("delete oauth2 claim map: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// GetOAuth2BasicSecret retrieves the client secret for a basic OAuth2 client
func (c *Client) GetOAuth2BasicSecret(ctx context.Context, name string) (string, error) {
	resp, err := c.doRequest(ctx, "GET", fmt.Sprintf("/v1/oauth2/%s/_basic_secret", name), nil)
	if err != nil {
		return "", fmt.Errorf("get oauth2 basic secret: %w", err)
	}

	// The API returns the secret as a plain JSON string
	var secret string
	if err := decodeResponse(resp, &secret); err != nil {
		return "", err
	}

	return secret, nil
}

// RegenerateOAuth2BasicSecret regenerates the client secret for a basic OAuth2 client
// This invalidates the old secret and generates a new one
func (c *Client) RegenerateOAuth2BasicSecret(ctx context.Context, name string) (string, error) {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/v1/oauth2/%s/_basic_secret", name), nil)
	if err != nil {
		return "", fmt.Errorf("regenerate oauth2 basic secret: %w", err)
	}

	// The API returns the new secret as a plain JSON string
	var secret string
	if err := decodeResponse(resp, &secret); err != nil {
		return "", err
	}

	return secret, nil
}
