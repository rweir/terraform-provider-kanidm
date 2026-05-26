package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// ServiceAccount represents a Kanidm service account
type ServiceAccount struct {
	ID             string
	DisplayName    string
	APIToken       string   // Only populated on creation
	EntryManagedBy []string // Account/group IDs that can manage this entry
	// MemberOf is the set of groups the account is a direct member
	// of. Populated from the `directmemberof` attribute on the entry
	// (which excludes inherited memberships via nested groups).
	MemberOf []string
}

// CreateServiceAccount creates a new service account
func (c *Client) CreateServiceAccount(ctx context.Context, name, displayName string, entryManagedBy []string) (*ServiceAccount, error) {
	attrs := map[string]any{
		"name": []string{name},
	}

	if displayName != "" {
		attrs["displayname"] = []string{displayName}
	}

	if len(entryManagedBy) > 0 {
		attrs["entry_managed_by"] = entryManagedBy
	}

	req := NewCreateRequest(attrs)

	resp, err := c.doRequest(ctx, "POST", "/v1/service_account", req)
	if err != nil {
		return nil, fmt.Errorf("create service account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	sa := &ServiceAccount{
		ID:             name,
		DisplayName:    displayName,
		EntryManagedBy: entryManagedBy,
	}

	// Generate initial API token
	token, err := c.GenerateServiceAccountToken(ctx, name, "terraform-managed", nil)
	if err != nil {
		return nil, fmt.Errorf("generate initial token: %w", err)
	}

	sa.APIToken = token

	return sa, nil
}

// GetServiceAccount retrieves a service account by ID
func (c *Client) GetServiceAccount(ctx context.Context, id string) (*ServiceAccount, error) {
	resp, err := c.doRequest(ctx, "GET", "/v1/service_account/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("get service account: %w", err)
	}

	var entry Entry
	if err := decodeResponse(resp, &entry); err != nil {
		return nil, err
	}

	// directmemberof comes back as SPN-form strings like
	// `idm_mail_servers@sso.erti.us`. Strip the domain so the values
	// round-trip cleanly against bare group names users typically
	// declare in HCL.
	directMemberOf := entry.GetStringSlice("directmemberof")
	memberOf := make([]string, 0, len(directMemberOf))
	for _, m := range directMemberOf {
		memberOf = append(memberOf, stripSPNDomain(m))
	}

	return &ServiceAccount{
		ID:             entry.GetString("name"),
		DisplayName:    entry.GetString("displayname"),
		EntryManagedBy: entry.GetStringSlice("entry_managed_by"),
		MemberOf:       memberOf,
		// Note: API tokens are not returned in GET responses
	}, nil
}

// AddMemberToGroup appends a single member to the named group's
// `member` attribute via a read-modify-write PATCH. Idempotent —
// adding an existing member is a no-op. Used by the service-account
// resource's `member_of` management to add the SA to groups (often
// kanidm builtins) without taking ownership of the group itself.
func (c *Client) AddMemberToGroup(ctx context.Context, groupID, memberID string) error {
	current, err := c.GetGroup(ctx, groupID)
	if err != nil {
		return fmt.Errorf("add member to group %q: read current: %w", groupID, err)
	}
	for _, m := range current.Members {
		if stripSPNDomain(m) == memberID || m == memberID {
			return nil // already there
		}
	}
	newMembers := append([]string{}, current.Members...)
	newMembers = append(newMembers, memberID)
	if err := c.UpdateGroup(ctx, groupID, "", newMembers); err != nil {
		return fmt.Errorf("add member to group %q: %w", groupID, err)
	}
	return nil
}

// RemoveMemberFromGroup removes a single member from the named
// group's `member` attribute via a read-modify-write PATCH. Idempotent.
func (c *Client) RemoveMemberFromGroup(ctx context.Context, groupID, memberID string) error {
	current, err := c.GetGroup(ctx, groupID)
	if err != nil {
		return fmt.Errorf("remove member from group %q: read current: %w", groupID, err)
	}
	newMembers := make([]string, 0, len(current.Members))
	removed := false
	for _, m := range current.Members {
		if stripSPNDomain(m) == memberID || m == memberID {
			removed = true
			continue
		}
		newMembers = append(newMembers, m)
	}
	if !removed {
		return nil // nothing to do
	}
	if err := c.UpdateGroup(ctx, groupID, "", newMembers); err != nil {
		return fmt.Errorf("remove member from group %q: %w", groupID, err)
	}
	return nil
}

// UpdateServiceAccount updates a service account
func (c *Client) UpdateServiceAccount(ctx context.Context, id, displayName string, entryManagedBy []string) error {
	attrs := make(map[string]any)

	if displayName != "" {
		attrs["displayname"] = []string{displayName}
	}

	if entryManagedBy != nil {
		attrs["entry_managed_by"] = entryManagedBy
	}

	req := NewUpdateRequest(attrs)

	resp, err := c.doRequest(ctx, "PATCH", "/v1/service_account/"+id, req)
	if err != nil {
		return fmt.Errorf("update service account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// DeleteServiceAccount deletes a service account
func (c *Client) DeleteServiceAccount(ctx context.Context, id string) error {
	resp, err := c.doRequest(ctx, "DELETE", "/v1/service_account/"+id, nil)
	if err != nil {
		return fmt.Errorf("delete service account: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// GenerateServiceAccountToken generates a new API token for the service account
func (c *Client) GenerateServiceAccountToken(ctx context.Context, id, label string, expiry *int64) (string, error) {
	req := map[string]any{
		"label":      label,
		"expiry":     nil,
		"read_write": true,
	}

	if expiry != nil {
		req["expiry"] = *expiry
	}

	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/v1/service_account/%s/_api_token", id), req)
	if err != nil {
		return "", fmt.Errorf("generate api token: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Read the response body first for better error reporting
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("read response body: %w", err)
	}

	// Try to unmarshal as JSON first
	var result struct {
		Token string `json:"token"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		// If JSON unmarshal fails, try treating the response as a plain string (JWT token)
		// Remove quotes if present (API returns quoted string)
		token := string(body)
		if len(token) > 0 && token[0] == '"' && token[len(token)-1] == '"' {
			token = token[1 : len(token)-1]
		}
		// Basic validation: JWT tokens should have at least 2 dots
		if len(token) > 0 && strings.Count(token, ".") >= 2 {
			return token, nil
		}
		return "", fmt.Errorf("decode response: %w (response body: %s)", err, string(body))
	}

	return result.Token, nil
}
