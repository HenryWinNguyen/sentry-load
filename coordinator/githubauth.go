package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

const (
	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubTokenURL     = "https://github.com/login/oauth/access_token"
	githubUserURL      = "https://api.github.com/user"
)

// oauthExchanger trades a GitHub OAuth authorization code for an access
// token. Extracted as an interface, same reason as verify.go's
// txtLookuper/httpGetter — tests inject a fake instead of hitting GitHub.
type oauthExchanger interface {
	Exchange(ctx context.Context, code string) (accessToken string, err error)
}

// githubUserFetcher fetches the identity of whoever an access token
// belongs to.
type githubUserFetcher interface {
	FetchUser(ctx context.Context, accessToken string) (githubID int64, githubLogin string, err error)
}

// githubOAuthClient is the production implementation of both interfaces,
// talking to real GitHub endpoints.
type githubOAuthClient struct {
	clientID     string
	clientSecret string
	redirectURL  string
	httpClient   *http.Client
}

var (
	_ oauthExchanger    = (*githubOAuthClient)(nil)
	_ githubUserFetcher = (*githubOAuthClient)(nil)
)

func (c *githubOAuthClient) authorizeURL(state string) string {
	q := url.Values{
		"client_id":    {c.clientID},
		"redirect_uri": {c.redirectURL},
		"state":        {state},
	}
	return githubAuthorizeURL + "?" + q.Encode()
}

func (c *githubOAuthClient) Exchange(ctx context.Context, code string) (string, error) {
	form := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.clientSecret},
		"code":          {code},
		"redirect_uri":  {c.redirectURL},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, githubTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", fmt.Errorf("building token exchange request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("exchanging code for token: %w", err)
	}
	defer resp.Body.Close()

	var body struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
		ErrorDesc   string `json:"error_description"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&body); err != nil {
		return "", fmt.Errorf("decoding token exchange response: %w", err)
	}
	if body.Error != "" {
		return "", fmt.Errorf("github rejected the code: %s (%s)", body.Error, body.ErrorDesc)
	}
	if body.AccessToken == "" {
		return "", fmt.Errorf("github returned no access token")
	}
	return body.AccessToken, nil
}

func (c *githubOAuthClient) FetchUser(ctx context.Context, accessToken string) (int64, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubUserURL, nil)
	if err != nil {
		return 0, "", fmt.Errorf("building user fetch request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return 0, "", fmt.Errorf("fetching github user: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, "", fmt.Errorf("github user fetch returned status %d", resp.StatusCode)
	}

	var body struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&body); err != nil {
		return 0, "", fmt.Errorf("decoding github user response: %w", err)
	}
	if body.ID == 0 || body.Login == "" {
		return 0, "", fmt.Errorf("github user response missing id/login")
	}
	return body.ID, body.Login, nil
}
