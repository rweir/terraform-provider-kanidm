package client

import (
	"context"
	"fmt"
	"strconv"
)

// Person represents a Kanidm person account
type Person struct {
	ID          string
	DisplayName string
	Mail        []string
	Legalname   string
	// Posix is true when the account has the `posixaccount` class.
	// Kanidm assigns GidNumber automatically (or uses an explicit
	// override) when the class is added; disabling is not supported.
	Posix      bool
	GidNumber  int64
	Loginshell string
}

// CreatePerson creates a new person account
func (c *Client) CreatePerson(ctx context.Context, name, displayName string) (*Person, error) {
	req := NewCreateRequest(map[string]any{
		"name":        []string{name},
		"displayname": []string{displayName},
	})

	resp, err := c.doRequest(ctx, "POST", "/v1/person", req)
	if err != nil {
		return nil, fmt.Errorf("create person: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// Return the created person
	return &Person{
		ID:          name,
		DisplayName: displayName,
	}, nil
}

// GetPerson retrieves a person account by ID
func (c *Client) GetPerson(ctx context.Context, id string) (*Person, error) {
	resp, err := c.doRequest(ctx, "GET", "/v1/person/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("get person: %w", err)
	}

	var entry Entry
	if err := decodeResponse(resp, &entry); err != nil {
		return nil, err
	}

	posix := false
	for _, cls := range entry.GetStringSlice("class") {
		if cls == "posixaccount" {
			posix = true
			break
		}
	}
	var gidNumber int64
	if s := entry.GetString("gidnumber"); s != "" {
		if v, err := strconv.ParseInt(s, 10, 64); err == nil {
			gidNumber = v
		}
	}

	return &Person{
		ID:          entry.GetString("name"),
		DisplayName: entry.GetString("displayname"),
		Mail:        entry.GetStringSlice("mail"),
		Legalname:   entry.GetString("legalname"),
		Posix:       posix,
		GidNumber:   gidNumber,
		Loginshell:  entry.GetString("loginshell"),
	}, nil
}

// UpdatePersonOpts is the set of attributes that can be PATCHed on a
// person. Fields whose Go zero value is "no change" follow that
// convention; for nullable string attrs we use *string so nil means
// "don't touch".
type UpdatePersonOpts struct {
	DisplayName string
	Mail        []string
	Legalname   *string
	// Loginshell PATCHes the `loginshell` attribute. Only valid when
	// the account already has `class: posixaccount` — call
	// EnablePersonPosix first.
	Loginshell *string
}

// UpdatePerson PATCHes the named person account. Only attributes
// present in opts are touched server-side, so out-of-band attributes
// the provider doesn't model are preserved.
func (c *Client) UpdatePerson(ctx context.Context, id string, opts UpdatePersonOpts) error {
	attrs := make(map[string]any)

	if opts.DisplayName != "" {
		attrs["displayname"] = []string{opts.DisplayName}
	}

	if opts.Mail != nil {
		attrs["mail"] = opts.Mail
	}

	if opts.Legalname != nil {
		// Kanidm clears the attribute with an empty array.
		if *opts.Legalname == "" {
			attrs["legalname"] = []string{}
		} else {
			attrs["legalname"] = []string{*opts.Legalname}
		}
	}

	if opts.Loginshell != nil {
		if *opts.Loginshell == "" {
			attrs["loginshell"] = []string{}
		} else {
			attrs["loginshell"] = []string{*opts.Loginshell}
		}
	}

	req := NewUpdateRequest(attrs)

	resp, err := c.doRequest(ctx, "PATCH", "/v1/person/"+id, req)
	if err != nil {
		return fmt.Errorf("update person: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// EnablePersonPosix adds the `posixaccount` class to a person, making
// it a POSIX-mapped account (visible via NSS / `kanidm_unixd`). If
// `gidNumber` or `loginshell` are non-nil, those values are used;
// otherwise Kanidm picks defaults (gidnumber auto-assigned from the
// UUID, loginshell defaults to `/bin/sh` or similar). Idempotent.
//
// Kanidm does NOT support removing the class once set.
func (c *Client) EnablePersonPosix(ctx context.Context, id string, gidNumber *int64, loginshell *string) error {
	body := map[string]any{}
	if gidNumber != nil {
		body["gidnumber"] = uint32(*gidNumber)
	}
	if loginshell != nil {
		// The AccountUnixExtend struct uses `shell` for this field.
		body["shell"] = *loginshell
	}
	resp, err := c.doRequest(ctx, "POST", "/v1/person/"+id+"/_unix", body)
	if err != nil {
		return fmt.Errorf("enable person posix: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// DeletePerson deletes a person account
func (c *Client) DeletePerson(ctx context.Context, id string) error {
	resp, err := c.doRequest(ctx, "DELETE", "/v1/person/"+id, nil)
	if err != nil {
		return fmt.Errorf("delete person: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// SetPersonPassword sets the password for a person account
func (c *Client) SetPersonPassword(ctx context.Context, id, password string) error {
	// Note: This uses the credential update intent API
	// Implementation will depend on Kanidm's exact credential management flow
	req := map[string]any{
		"password": password,
	}

	resp, err := c.doRequest(ctx, "POST", fmt.Sprintf("/v1/person/%s/_credential/_update_intent", id), req)
	if err != nil {
		return fmt.Errorf("set person password: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	return nil
}

// CreatePersonCredentialResetToken creates a credential reset token for passkey/password setup via UI
// This enables the modern Kanidm workflow: create person -> generate token -> user sets up credentials
// The ttl parameter is optional and specifies the token lifetime in seconds
func (c *Client) CreatePersonCredentialResetToken(ctx context.Context, id string, ttl *int) (string, error) {
	path := fmt.Sprintf("/v1/person/%s/_credential/_update_intent", id)
	if ttl != nil {
		path = fmt.Sprintf("/v1/person/%s/_credential/_update_intent/%d", id, *ttl)
	}

	resp, err := c.doRequest(ctx, "GET", path, nil)
	if err != nil {
		return "", fmt.Errorf("create credential reset token: %w", err)
	}

	var result struct {
		Token string `json:"token"`
	}

	if err := decodeResponse(resp, &result); err != nil {
		return "", err
	}

	return result.Token, nil
}
