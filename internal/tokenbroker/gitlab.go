package tokenbroker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type CreatedToken struct {
	ID    string
	Token string
	Name  string
}

type GitLabClient struct {
	baseURL    string
	controlPAT string
	httpClient *http.Client
}

func NewGitLabClient(baseURL, controlPAT string, httpClient *http.Client) *GitLabClient {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	return &GitLabClient{
		baseURL:    strings.TrimRight(baseURL, "/"),
		controlPAT: controlPAT,
		httpClient: httpClient,
	}
}

func (c *GitLabClient) CreateProjectToken(ctx context.Context, projectID, name string, expiresAt time.Time) (CreatedToken, error) {
	form := url.Values{}
	form.Set("name", name)
	form.Add("scopes[]", "read_repository")
	form.Add("scopes[]", "write_repository")
	form.Set("access_level", "30")
	form.Set("expires_at", expiresAt.UTC().Format("2006-01-02"))

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		c.baseURL+"/api/v4/projects/"+url.PathEscape(projectID)+"/access_tokens",
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return CreatedToken{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("PRIVATE-TOKEN", c.controlPAT)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return CreatedToken{}, err
	}
	defer resp.Body.Close()

	if !is2xx(resp.StatusCode) {
		return CreatedToken{}, fmt.Errorf("gitlab create project token failed: status %d", resp.StatusCode)
	}

	var response struct {
		ID    json.RawMessage `json:"id"`
		Token string          `json:"token"`
		Name  string          `json:"name"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return CreatedToken{}, err
	}

	id, err := decodeGitLabID(response.ID)
	if err != nil {
		return CreatedToken{}, err
	}

	return CreatedToken{
		ID:    id,
		Token: response.Token,
		Name:  response.Name,
	}, nil
}

func (c *GitLabClient) RevokeProjectToken(ctx context.Context, projectID, tokenID string) error {
	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodDelete,
		c.baseURL+"/api/v4/projects/"+url.PathEscape(projectID)+"/access_tokens/"+url.PathEscape(tokenID),
		nil,
	)
	if err != nil {
		return err
	}
	req.Header.Set("PRIVATE-TOKEN", c.controlPAT)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if !is2xx(resp.StatusCode) {
		return fmt.Errorf("gitlab revoke project token failed: status %d", resp.StatusCode)
	}

	return nil
}

func decodeGitLabID(raw json.RawMessage) (string, error) {
	var number int64
	if err := json.Unmarshal(raw, &number); err == nil {
		return strconv.FormatInt(number, 10), nil
	}

	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		return text, nil
	}

	return "", fmt.Errorf("gitlab token response has invalid id")
}

func is2xx(statusCode int) bool {
	return statusCode >= 200 && statusCode <= 299
}
