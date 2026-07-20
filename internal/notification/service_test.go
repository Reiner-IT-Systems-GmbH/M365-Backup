package notification_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rhw/m365backup/internal/db"
	"github.com/rhw/m365backup/internal/notification"
)

func TestWebhookDispatch(t *testing.T) {
	var gotBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	dir := t.TempDir()
	database, err := db.Open(db.Options{Driver: db.DriverSQLite, SQLitePath: dir + "/test.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	cfg, _ := json.Marshal(map[string]string{"url": srv.URL, "format": "json"})
	on, _ := json.Marshal([]string{"job_error"})
	if err := database.UpsertNotificationSetting(context.Background(), &db.NotificationSetting{
		Channel: "webhook", Enabled: true, Config: string(cfg), NotifyOn: string(on),
	}); err != nil {
		t.Fatal(err)
	}

	n := notification.New(database, slog.Default())
	n.Client = srv.Client()
	if err := n.Send(context.Background(), notification.Event{
		Type: notification.EventJobError, Subject: "fail", Body: "boom",
	}); err != nil {
		t.Fatal(err)
	}
	if len(gotBody) == 0 {
		t.Fatal("expected webhook body")
	}
}

func TestPushoverDispatch(t *testing.T) {
	var gotForm string
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		body, _ := io.ReadAll(r.Body)
		gotForm = string(body)
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"status":1}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	database, err := db.Open(db.Options{Driver: db.DriverSQLite, SQLitePath: dir + "/test.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()

	cfg, _ := json.Marshal(map[string]any{
		"user_key": "uQiRzpo4DXghDmr9QzzfQu27cmVRsG",
		"app_token": "azGDORePK8gMaC0QOYAMyEEuzJnyUi",
		"title": "M365 Backup",
		"priority": 1,
		"sound": "siren",
		"sound_ok": "magic",
	})
	on, _ := json.Marshal([]string{"job_error", "job_success"})
	if err := database.UpsertNotificationSetting(context.Background(), &db.NotificationSetting{
		Channel: "pushover", Enabled: true, Config: string(cfg), NotifyOn: string(on),
	}); err != nil {
		t.Fatal(err)
	}

	n := notification.New(database, slog.Default())
	// Point Client at test server by rewriting via custom transport isn't needed —
	// we inject by temporarily using a client that we can't redirect API host easily.
	// Instead call Send after patching via a helper: use RoundTripper rewrite.
	n.Client = &http.Client{
		Transport: roundTripRewrite{to: srv.URL, base: http.DefaultTransport},
		Timeout:   5 * time.Second,
	}

	if err := n.Send(context.Background(), notification.Event{
		Type: notification.EventJobError, Subject: "fail", Body: "boom",
	}); err != nil {
		t.Fatal(err)
	}
	if gotPath != "/1/messages.json" {
		t.Fatalf("path = %q", gotPath)
	}
	if !strings.Contains(gotForm, "sound=siren") {
		t.Fatalf("expected alert sound, got %q", gotForm)
	}
	if !strings.Contains(gotForm, "priority=1") {
		t.Fatalf("expected priority, got %q", gotForm)
	}

	gotForm = ""
	if err := n.Send(context.Background(), notification.Event{
		Type: notification.EventJobSuccess, Subject: "ok", Body: "done",
	}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(gotForm, "sound=magic") {
		t.Fatalf("expected ok sound, got %q", gotForm)
	}
}

type roundTripRewrite struct {
	to   string
	base http.RoundTripper
}

func (r roundTripRewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u, err := url.Parse(r.to)
	if err != nil {
		return nil, err
	}
	req2 := req.Clone(req.Context())
	req2.URL.Scheme = u.Scheme
	req2.URL.Host = u.Host
	req2.Host = u.Host
	return r.base.RoundTrip(req2)
}
