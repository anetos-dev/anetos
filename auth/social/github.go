// SPDX-License-Identifier: Apache-2.0

package social

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"golang.org/x/oauth2"
)

// GitHubAPI is the base URL of GitHub's API, which [GitHub] reads
// profiles from (GitHub Enterprise Server: https://HOST/api/v3).
const GitHubAPI = "https://api.github.com"

// GitHub is GitHub's sign-in (OAuth 2.0), asking to read the profile and
// email addresses. The profile's email is the account's primary address,
// if GitHub has verified it.
func GitHub() Provider {
	return GitHubAt("https://github.com", GitHubAPI)
}

// GitHubAt is GitHub's sign-in on another host (GitHub Enterprise
// Server): web is its site, api its API's base URL.
func GitHubAt(web, api string) Provider {
	return Provider{
		Name:  "github",
		Title: "GitHub",
		Endpoint: oauth2.Endpoint{
			AuthURL:  web + "/login/oauth/authorize",
			TokenURL: web + "/login/oauth/access_token",
		},
		Scopes: []string{"read:user", "user:email"},
		API:    api,
		Profile: func(ctx context.Context, client *http.Client, tok *oauth2.Token) (Profile, error) {
			var u struct {
				ID        int64  `json:"id"`
				Login     string `json:"login"`
				Name      string `json:"name"`
				AvatarURL string `json:"avatar_url"`
			}
			if err := githubGet(ctx, client, tok, api+"/user", &u); err != nil {
				return Profile{}, err
			}
			if u.ID == 0 {
				return Profile{}, fmt.Errorf("no user id")
			}
			p := Profile{Subject: strconv.FormatInt(u.ID, 10), Name: u.Name, AvatarURL: u.AvatarURL}
			if p.Name == "" {
				p.Name = u.Login
			}
			var emails []struct {
				Email    string `json:"email"`
				Primary  bool   `json:"primary"`
				Verified bool   `json:"verified"`
			}
			if err := githubGet(ctx, client, tok, api+"/user/emails", &emails); err != nil {
				return Profile{}, err
			}
			for _, e := range emails {
				if e.Primary {
					p.Email, p.EmailVerified = e.Email, e.Verified
				}
			}
			return p, nil
		},
	}
}

func githubGet(ctx context.Context, client *http.Client, tok *oauth2.Token, u string, dst any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Authorization", "Bearer "+tok.AccessToken)
	res, err := client.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: %s", u, res.Status)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(dst)
}
