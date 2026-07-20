package graph

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	msgraph "github.com/microsoftgraph/msgraph-sdk-go"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/users"
)

// Client wraps Microsoft Graph with app-only credentials.
type Client struct {
	Graph  *msgraph.GraphServiceClient
	HTTP   *http.Client
	Token  azcore.TokenCredential
	Tenant string
}

func New(ctx context.Context, tenantID, clientID, clientSecret string) (*Client, error) {
	cred, err := azidentity.NewClientSecretCredential(tenantID, clientID, clientSecret, nil)
	if err != nil {
		return nil, fmt.Errorf("azure credential: %w", err)
	}
	gc, err := msgraph.NewGraphServiceClientWithCredentials(cred, []string{"https://graph.microsoft.com/.default"})
	if err != nil {
		return nil, fmt.Errorf("graph client: %w", err)
	}
	return &Client{
		Graph:  gc,
		HTTP:   &http.Client{Timeout: 15 * time.Minute}, // large MIME downloads
		Token:  cred,
		Tenant: tenantID,
	}, nil
}

func (c *Client) accessToken(ctx context.Context) (string, error) {
	tok, err := c.Token.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{"https://graph.microsoft.com/.default"},
	})
	if err != nil {
		return "", err
	}
	return tok.Token, nil
}

// GetBytes fetches an arbitrary Graph URL (e.g. @microsoft.graph.downloadUrl or $value).
func (c *Client) GetBytes(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	tok, err := c.accessToken(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("GET %s: status %d: %s", rawURL, resp.StatusCode, truncate(string(body), 200))
	}
	return body, nil
}

// ListUsers returns all directory users that may have a mailbox (paginated).
// Includes shared mailboxes: in Entra they are user objects with accountEnabled=false.
func (c *Client) ListUsers(ctx context.Context) ([]models.Userable, error) {
	top := int32(999)
	// Prefer accounts with a mail attribute (user + shared + room/equipment with SMTP).
	// Do NOT filter accountEnabled — shared mailboxes are typically disabled for sign-in.
	filter := "mail ne null"
	cfg := &users.UsersRequestBuilderGetRequestConfiguration{
		QueryParameters: &users.UsersRequestBuilderGetQueryParameters{
			Top:    &top,
			Filter: &filter,
			Select: []string{"id", "userPrincipalName", "mail", "displayName", "accountEnabled"},
		},
	}
	resp, err := c.Graph.Users().Get(ctx, cfg)
	if err != nil {
		// Fallback: no filter (full directory), still paginated
		resp, err = c.Graph.Users().Get(ctx, &users.UsersRequestBuilderGetRequestConfiguration{
			QueryParameters: &users.UsersRequestBuilderGetQueryParameters{
				Top:    &top,
				Select: []string{"id", "userPrincipalName", "mail", "displayName", "accountEnabled"},
			},
		})
		if err != nil {
			return nil, err
		}
	}

	var all []models.Userable
	for {
		all = append(all, resp.GetValue()...)
		next := resp.GetOdataNextLink()
		if next == nil || *next == "" {
			break
		}
		resp, err = c.Graph.Users().WithUrl(*next).Get(ctx, nil)
		if err != nil {
			return all, fmt.Errorf("users next page: %w", err)
		}
	}
	return all, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
