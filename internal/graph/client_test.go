package graph

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

type stubToken struct{}

func (stubToken) GetToken(context.Context, policy.TokenRequestOptions) (azcore.AccessToken, error) {
	return azcore.AccessToken{Token: "test-token", ExpiresOn: time.Now().Add(time.Hour)}, nil
}

func TestTimeoutsInRange(t *testing.T) {
	if SDKRequestTimeout < 60*time.Second || SDKRequestTimeout > 120*time.Second {
		t.Fatalf("SDKRequestTimeout=%s want 60s–120s", SDKRequestTimeout)
	}
	if MIMETimeout < 2*time.Minute || MIMETimeout > 5*time.Minute {
		t.Fatalf("MIMETimeout=%s want 2–5 min", MIMETimeout)
	}
}

func TestIsRetryableGetBytesErr(t *testing.T) {
	if isRetryableGetBytesErr(nil) {
		t.Fatal("nil")
	}
	if isRetryableGetBytesErr(context.Canceled) {
		t.Fatal("canceled must not retry")
	}
	if !isRetryableGetBytesErr(context.DeadlineExceeded) {
		t.Fatal("deadline must retry")
	}
	if !isRetryableGetBytesErr(fmt.Errorf("GET x: status 503: busy")) {
		t.Fatal("503")
	}
	if !isRetryableGetBytesErr(fmt.Errorf("GET x: status 429: slow")) {
		t.Fatal("429")
	}
	if isRetryableGetBytesErr(fmt.Errorf("GET x: status 404: missing")) {
		t.Fatal("404 must not retry")
	}
	if isRetryableGetBytesErr(fmt.Errorf("GET x: status 400: ErrorMimeContentConversionFailed")) {
		t.Fatal("MIME conversion must not retry")
	}
}

func TestGetBytesRetriesThenSucceeds(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("busy"))
			return
		}
		_, _ = w.Write([]byte("ok-body"))
	}))
	t.Cleanup(srv.Close)

	c := &Client{
		HTTP:  &http.Client{Timeout: 2 * time.Second},
		Token: stubToken{},
	}
	body, err := c.GetBytes(context.Background(), srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "ok-body" {
		t.Fatalf("body=%q", body)
	}
	if n.Load() != 2 {
		t.Fatalf("attempts=%d", n.Load())
	}
}

func TestGetBytesDoesNotRetryNotFound(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("missing"))
	}))
	t.Cleanup(srv.Close)

	c := &Client{
		HTTP:  &http.Client{Timeout: 2 * time.Second},
		Token: stubToken{},
	}
	_, err := c.GetBytes(context.Background(), srv.URL)
	if err == nil {
		t.Fatal("expected error")
	}
	if n.Load() != 1 {
		t.Fatalf("attempts=%d", n.Load())
	}
}

func TestWithSDKTimeout(t *testing.T) {
	ctx, cancel := WithSDKTimeout(context.Background())
	defer cancel()
	dl, ok := ctx.Deadline()
	if !ok {
		t.Fatal("expected deadline")
	}
	remain := time.Until(dl)
	if remain < SDKRequestTimeout-time.Second || remain > SDKRequestTimeout+time.Second {
		t.Fatalf("remain=%s", remain)
	}
}
