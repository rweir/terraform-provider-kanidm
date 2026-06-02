package client

import (
	"context"
	"fmt"
	"strconv"
)

// Group represents a Kanidm group
type Group struct {
	ID          string
	Description string
	Members     []string
	// Posix is true when the group has the `posixgroup` class.
	// Kanidm assigns GidNumber automatically when the class is added;
	// disabling is not supported by the kanidm API.
	Posix     bool
	GidNumber int64

	// AccountPolicy is true when the group has the `account_policy`
	// class, allowing account-policy attributes to be set on it.
	AccountPolicy bool

	// AuthSessionExpiry mirrors Kanidm's `authsession_expiry`
	// account-policy attribute. The Set flag distinguishes absent from
	// explicitly zero.
	AuthSessionExpiry    int64
	AuthSessionExpirySet bool
}

// CreateGroup creates a new group
func (c *Client) CreateGroup(ctx context.Context, name, description string) (*Group, error) {
	attrs := map[string]any{
		"name": []string{name},
	}

	if description != "" {
		attrs["description"] = []string{description}
	}

	req := NewCreateRequest(attrs)

	resp, err := c.doRequest(ctx, "POST", "/v1/group", req)
	if err != nil {
		return nil, fmt.Errorf("create group: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return &Group{
		ID:          name,
		Description: description,
	}, nil
}

// GetGroup retrieves a group by ID
func (c *Client) GetGroup(ctx context.Context, id string) (*Group, error) {
	resp, err := c.doRequest(ctx, "GET", "/v1/group/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("get group: %w", err)
	}

	var entry Entry
	if err := decodeResponse(resp, &entry); err != nil {
		return nil, err
	}

	// Ensure members is never nil
	members := entry.GetStringSlice("member")
	if members == nil {
		members = []string{}
	}

	posix := false
	accountPolicy := false
	for _, cls := range entry.GetStringSlice("class") {
		if cls == "posixgroup" {
			posix = true
		}
		if cls == "account_policy" {
			accountPolicy = true
		}
	}

	var gidNumber int64
	if s := entry.GetString("gidnumber"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			gidNumber = v
		}
	}
	authSessionExpiry, authSessionExpirySet := entry.GetInt64("authsession_expiry")

	return &Group{
		ID:                   entry.GetString("name"),
		Description:          entry.GetString("description"),
		Members:              members,
		Posix:                posix,
		GidNumber:            gidNumber,
		AccountPolicy:        accountPolicy,
		AuthSessionExpiry:    authSessionExpiry,
		AuthSessionExpirySet: authSessionExpirySet,
	}, nil
}

// UpdateGroup updates a group
func (c *Client) UpdateGroup(ctx context.Context, id, description string, members []string) error {
	attrs := make(map[string]any)

	if description != "" {
		attrs["description"] = []string{description}
	}

	if members != nil {
		attrs["member"] = members
	}

	req := NewUpdateRequest(attrs)

	resp, err := c.doRequest(ctx, "PATCH", "/v1/group/"+id, req)
	if err != nil {
		return fmt.Errorf("update group: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// SetGroupAuthSessionExpiry sets the account-policy auth session
// expiry for a group. Kanidm expects generic attribute values as
// string arrays.
func (c *Client) SetGroupAuthSessionExpiry(ctx context.Context, groupID string, expiry int64) error {
	resp, err := c.doRequest(ctx, "PUT", fmt.Sprintf("/v1/group/%s/_attr/authsession_expiry", groupID), []string{strconv.FormatInt(expiry, 10)})
	if err != nil {
		return fmt.Errorf("set group auth session expiry: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// EnableGroupAccountPolicy adds the `account_policy` class to a group.
// Kanidm requires this class before account-policy attrs can be set.
func (c *Client) EnableGroupAccountPolicy(ctx context.Context, groupID string) error {
	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/v1/group/%s/_attr/class", groupID), []string{"account_policy"})
	if err != nil {
		return fmt.Errorf("enable group account policy: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// ResetGroupAuthSessionExpiry removes the group-specific
// authsession_expiry attribute, letting Kanidm defaults/policy
// resolution apply.
func (c *Client) ResetGroupAuthSessionExpiry(ctx context.Context, groupID string) error {
	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/v1/group/%s/_attr/authsession_expiry", groupID), nil)
	if err != nil {
		return fmt.Errorf("reset group auth session expiry: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// DeleteGroup deletes a group
func (c *Client) DeleteGroup(ctx context.Context, id string) error {
	resp, err := c.doRequest(ctx, "DELETE", "/v1/group/"+id, nil)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// EnableGroupPosix adds the `posixgroup` class to a group, which makes
// it visible to nss/kanidm-unixd. If `gidnumber` is nil, Kanidm
// auto-assigns one from the entry's UUID; if non-nil, the caller's
// value is used (typical valid explicit range is 65536–524287).
// Idempotent — calling on an already-POSIX group is a no-op.
//
// Kanidm does NOT support removing the class once set; there is no
// corresponding DisableGroupPosix.
func (c *Client) EnableGroupPosix(ctx context.Context, groupID string, gidNumber *int64) error {
	body := map[string]any{}
	if gidNumber != nil {
		// The /_unix endpoint takes gidnumber as a raw u32, NOT the
		// array-of-strings convention used by the generic attribute
		// API. (Kanidm's GroupUnixExtend struct fields are typed.)
		body["gidnumber"] = uint32(*gidNumber)
	}
	resp, err := c.doRequest(ctx, "POST", "/v1/group/"+groupID+"/_unix", body)
	if err != nil {
		return fmt.Errorf("enable group posix: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// AddGroupMembers adds members to a group
func (c *Client) AddGroupMembers(ctx context.Context, groupID string, memberIDs []string) error {
	// Use the attribute endpoint to add members
	req := map[string]any{
		"attrs": memberIDs,
	}

	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/v1/group/%s/_attr/member", groupID), req)
	if err != nil {
		return fmt.Errorf("add group members: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// RemoveGroupMembers removes members from a group
func (c *Client) RemoveGroupMembers(ctx context.Context, groupID string, memberIDs []string) error {
	// Use the attribute endpoint to remove members
	req := map[string]any{
		"attrs": memberIDs,
	}

	resp, err := c.doRequest(ctx, "DELETE", fmt.Sprintf("/v1/group/%s/_attr/member", groupID), req)
	if err != nil {
		return fmt.Errorf("remove group members: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}
