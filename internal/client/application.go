package client

import (
	"context"
	"fmt"
)

// Application service accounts live under the SCIM v1 API
// (`/scim/v1/Application`), not the legacy `/v1` paths the other
// resources use. They have classes `{account, application,
// service_account}` plus a `linked_group` reference that gates which
// users may mint per-application credentials.
type Application struct {
	ID          string // SCIM id == kanidm uuid
	Name        string
	DisplayName string
	LinkedGroup string // bare group name; the API returns SPN
}

// scimApplicationCreateReq is the snake_case body shape for POST
// /scim/v1/Application. The server adds the relevant classes itself.
type scimApplicationCreateReq struct {
	Name        string        `json:"name"`
	DisplayName string        `json:"displayname"`
	LinkedGroup scimReference `json:"linked_group"`
}

// scimReference matches kanidm's ScimReference shape. Either uuid or
// value (group name or SPN) is acceptable; we use `value` so callers
// can pass plain group names.
type scimReference struct {
	UUID  *string `json:"uuid,omitempty"`
	Value *string `json:"value,omitempty"`
}

// scimApplicationEntry is the camelCase response shape from
// /scim/v1/Application. linked_group comes back as an array of refs
// (matching SCIM multi-valued reference attributes).
type scimApplicationEntry struct {
	ID          string                  `json:"id"`
	Name        string                  `json:"name"`
	DisplayName string                  `json:"displayname"`
	LinkedGroup []scimReferenceResponse `json:"linked_group"`
}

type scimReferenceResponse struct {
	UUID  string `json:"uuid"`
	Value string `json:"value"`
}

// CreateApplication creates a new application service account and
// returns its SCIM id (== uuid).
func (c *Client) CreateApplication(ctx context.Context, name, displayName, linkedGroup string) (*Application, error) {
	body := scimApplicationCreateReq{
		Name:        name,
		DisplayName: displayName,
		LinkedGroup: scimReference{Value: &linkedGroup},
	}
	resp, err := c.doRequest(ctx, "POST", "/scim/v1/Application", body)
	if err != nil {
		return nil, fmt.Errorf("create application: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var entry scimApplicationEntry
	if err := decodeResponse(resp, &entry); err != nil {
		return nil, fmt.Errorf("create application: decode response: %w", err)
	}
	return applicationFromScim(&entry), nil
}

// GetApplication reads an application by name or UUID.
func (c *Client) GetApplication(ctx context.Context, id string) (*Application, error) {
	resp, err := c.doRequest(ctx, "GET", "/scim/v1/Application/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("get application: %w", err)
	}

	var entry scimApplicationEntry
	if err := decodeResponse(resp, &entry); err != nil {
		return nil, fmt.Errorf("get application: decode response: %w", err)
	}
	return applicationFromScim(&entry), nil
}

// scimEntryPutReq is the body shape for PUT /scim/v1/Entry. Fields
// set to non-nil are written; nil fields are left alone. (A literal
// `null` would delete, but our json:omitempty handling means we only
// send fields we intend to touch.)
type scimEntryPutReq struct {
	ID          string           `json:"id"`
	DisplayName *string          `json:"displayname,omitempty"`
	LinkedGroup *[]scimReference `json:"linked_group,omitempty"`
}

// UpdateApplication patches the named application. `id` here is the
// SCIM id (== uuid). DisplayName and/or LinkedGroup may be nil to
// leave them untouched.
func (c *Client) UpdateApplication(ctx context.Context, id string, displayName, linkedGroup *string) error {
	body := scimEntryPutReq{ID: id}
	if displayName != nil {
		body.DisplayName = displayName
	}
	if linkedGroup != nil {
		refs := []scimReference{{Value: linkedGroup}}
		body.LinkedGroup = &refs
	}
	if body.DisplayName == nil && body.LinkedGroup == nil {
		return nil
	}
	resp, err := c.doRequest(ctx, "PUT", "/scim/v1/Entry", body)
	if err != nil {
		return fmt.Errorf("update application: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

// DeleteApplication deletes an application by name or UUID.
func (c *Client) DeleteApplication(ctx context.Context, id string) error {
	resp, err := c.doRequest(ctx, "DELETE", "/scim/v1/Application/"+id, nil)
	if err != nil {
		return fmt.Errorf("delete application: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	return nil
}

// NOTE: per-user application passwords are intentionally not exposed
// in this client. Kanidm's ACP for `Attribute::ApplicationPassword`
// is `IDM_ACP_SELF_WRITE`, which only allows a person to modify their
// OWN application passwords. The provider authenticates with a single
// idm_admin token, so it can't pose as each user to mint their
// passwords. The intended setup is:
//
//   1. Tofu manages the kanidm_application (creates the SA + linked_group).
//   2. Each user runs `kanidm person credential application-password
//      create <user> <app> <label> --name <user>@<domain>` themselves
//      to mint their per-application password.

func applicationFromScim(e *scimApplicationEntry) *Application {
	a := &Application{
		ID:          e.ID,
		Name:        e.Name,
		DisplayName: e.DisplayName,
	}
	if len(e.LinkedGroup) > 0 {
		// LinkedGroup.Value comes back as `groupname@spn`; strip the
		// SPN suffix to match what tofu configs typically pass in.
		v := e.LinkedGroup[0].Value
		a.LinkedGroup = stripSPNDomain(v)
	}
	return a
}

