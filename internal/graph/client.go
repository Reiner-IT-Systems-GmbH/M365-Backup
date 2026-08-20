package graph

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	khttp "github.com/microsoft/kiota-http-go"
	msgraph "github.com/microsoftgraph/msgraph-sdk-go"
	az "github.com/microsoftgraph/msgraph-sdk-go-core/authentication"
	"github.com/microsoftgraph/msgraph-sdk-go/models"
	"github.com/microsoftgraph/msgraph-sdk-go/users"
)

const (
	// SDKRequestTimeout bounds Graph SDK list/delta pages (not MIME downloads).
	SDKRequestTimeout = 90 * time.Second
	// MIMETimeout bounds GET /$value and /content. Transient failures retry a few times.
	MIMETimeout     = 3 * time.Minute
	mimeMaxAttempts = 3
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
	auth, err := az.NewAzureIdentityAuthenticationProviderWithScopes(cred, []string{"https://graph.microsoft.com/.default"})
	if err != nil {
		return nil, fmt.Errorf("graph auth: %w", err)
	}
	sdkClient := khttp.GetDefaultClient()
	sdkClient.Timeout = SDKRequestTimeout
	adapter, err := msgraph.NewGraphRequestAdapterWithParseNodeFactoryAndSerializationWriterFactoryAndHttpClient(auth, nil, nil, sdkClient)
	if err != nil {
		return nil, fmt.Errorf("graph adapter: %w", err)
	}
	gc := msgraph.NewGraphServiceClient(adapter)
	return &Client{
		Graph:  gc,
		HTTP:   &http.Client{Timeout: MIMETimeout},
		Token:  cred,
		Tenant: tenantID,
	}, nil
}

// WithSDKTimeout caps a single Graph SDK request so a hung page does not stall the job.
func WithSDKTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, SDKRequestTimeout)
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
	var lastErr error
	for attempt := 1; attempt <= mimeMaxAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		body, err := c.getBytesOnce(ctx, rawURL)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !isRetryableGetBytesErr(err) || attempt == mimeMaxAttempts {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Duration(attempt) * time.Second):
		}
	}
	return nil, lastErr
}

func (c *Client) getBytesOnce(ctx context.Context, rawURL string) ([]byte, error) {
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

func isRetryableGetBytesErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	s := strings.ToLower(err.Error())
	switch {
	case strings.Contains(s, "status 429"),
		strings.Contains(s, "status 500"),
		strings.Contains(s, "status 503"),
		strings.Contains(s, "status 504"),
		strings.Contains(s, "connection reset"),
		strings.Contains(s, "unexpected eof"):
		return true
	default:
		return false
	}
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
		pageCtx, cancel := WithSDKTimeout(ctx)
		resp, err = c.Graph.Users().WithUrl(*next).Get(pageCtx, nil)
		cancel()
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
