package notification

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"

	"github.com/rhw/m365backup/internal/db"
)

const (
	EventJobError      = "job_error"
	EventJobWarning    = "job_warning"
	EventJobSuccess    = "job_success"
	EventKeyExpiry30D  = "key_expiry_30d"
	EventKeyExpiry7D   = "key_expiry_7d"
	EventKeyExpired    = "key_expired"
	EventQuotaWarning  = "quota_warning"
	EventRestoreDone   = "restore_done"
)

type Event struct {
	Type     string
	TenantID string
	Subject  string
	Body     string
}

type Service struct {
	DB     *db.DB
	Log    *slog.Logger
	Client *http.Client
	// Optional env-level SMTP fallback
	SMTPHost     string
	SMTPPort     int
	SMTPUser     string
	SMTPPassword string
	SMTPFrom     string
	SMTPTo       []string
}

func New(database *db.DB, log *slog.Logger) *Service {
	return &Service{
		DB:     database,
		Log:    log,
		Client: newWebhookHTTPClient(),
	}
}

func (s *Service) Send(ctx context.Context, ev Event) error {
	settings, err := s.DB.ListNotificationSettings(ctx)
	if err != nil {
		return err
	}
	matched := false
	for _, st := range settings {
		if !st.Enabled {
			continue
		}
		if st.TenantID != "" && st.TenantID != ev.TenantID {
			continue
		}
		var on []string
		_ = json.Unmarshal([]byte(st.NotifyOn), &on)
		if !contains(on, ev.Type) {
			continue
		}
		matched = true
		var sendErr error
		switch st.Channel {
		case "smtp":
			sendErr = s.sendSMTP(st.Config, ev)
		case "webhook", "slack", "teams":
			sendErr = s.sendWebhook(ctx, st.Channel, st.Config, ev)
		case "pushover":
			sendErr = s.sendPushover(ctx, st.Config, ev)
		default:
			sendErr = fmt.Errorf("unknown channel %s", st.Channel)
		}
		_ = s.DB.InsertNotificationLog(ctx, &db.NotificationLog{
			TenantID:  ev.TenantID,
			Channel:   st.Channel,
			EventType: ev.Type,
			Subject:   ev.Subject,
			Success:   sendErr == nil,
			Error:     errString(sendErr),
		})
		if sendErr != nil {
			s.Log.Error("notification failed", "channel", st.Channel, "err", sendErr)
		}
	}
	// Env SMTP fallback for errors/key events when no settings configured
	if !matched && s.SMTPHost != "" && len(s.SMTPTo) > 0 {
		if ev.Type == EventJobError || strings.HasPrefix(ev.Type, "key_") {
			cfg, _ := json.Marshal(map[string]any{
				"host": s.SMTPHost, "port": s.SMTPPort, "username": s.SMTPUser,
				"password": s.SMTPPassword, "from": s.SMTPFrom, "to": s.SMTPTo,
			})
			return s.sendSMTP(string(cfg), ev)
		}
	}
	return nil
}

type smtpConfig struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	Password string   `json:"password"`
	From     string   `json:"from"`
	To       []string `json:"to"`
}

func (s *Service) sendSMTP(configJSON string, ev Event) error {
	var cfg smtpConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return err
	}
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	subject := sanitizeHeader(ev.Subject)
	from := sanitizeHeader(cfg.From)
	toHeader := sanitizeHeader(strings.Join(cfg.To, ","))
	msg := []byte("From: " + from + "\r\n" +
		"To: " + toHeader + "\r\n" +
		"Subject: " + subject + "\r\n" +
		"MIME-Version: 1.0\r\nContent-Type: text/plain; charset=UTF-8\r\n\r\n" +
		ev.Body + "\r\n")
	var auth smtp.Auth
	if cfg.Username != "" {
		auth = smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
	}
	return smtp.SendMail(addr, auth, cfg.From, cfg.To, msg)
}

type webhookConfig struct {
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers"`
	Format  string            `json:"format"` // slack | teams | json
}

func (s *Service) sendWebhook(ctx context.Context, channel, configJSON string, ev Event) error {
	var cfg webhookConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return err
	}
	if err := ValidateWebhookURL(cfg.URL); err != nil {
		return err
	}
	format := cfg.Format
	if format == "" {
		format = channel
	}
	var body []byte
	switch format {
	case "slack":
		body, _ = json.Marshal(map[string]string{"text": "*" + ev.Subject + "*\n" + ev.Body})
	case "teams":
		body, _ = json.Marshal(map[string]any{
			"@type": "MessageCard", "summary": ev.Subject,
			"themeColor": "E81123", "title": ev.Subject, "text": ev.Body,
		})
	default:
		body, _ = json.Marshal(map[string]string{"subject": ev.Subject, "body": ev.Body, "type": ev.Type})
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range cfg.Headers {
		req.Header.Set(k, v)
	}
	client := s.Client
	if client == nil {
		client = newWebhookHTTPClient()
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook status %d", resp.StatusCode)
	}
	return nil
}

// pushoverConfig mirrors Uptime Kuma's Pushover options (priority, sounds, TTL).
type pushoverConfig struct {
	UserKey  string `json:"user_key"`
	AppToken string `json:"app_token"`
	Device   string `json:"device"`
	Title    string `json:"title"`
	Priority int    `json:"priority"` // -2..2; 2 = emergency (needs retry/expire)
	Sound    string `json:"sound"`    // alert / failure events
	SoundOK  string `json:"sound_ok"` // success / restore events
	TTL      int    `json:"ttl"`      // seconds; 0 = omit
	Retry    int    `json:"retry"`    // emergency retry interval (min 30)
	Expire   int    `json:"expire"`   // emergency expire (max 86400)
}

func (s *Service) sendPushover(ctx context.Context, configJSON string, ev Event) error {
	var cfg pushoverConfig
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return err
	}
	if cfg.UserKey == "" || cfg.AppToken == "" {
		return fmt.Errorf("pushover user_key and app_token required")
	}
	title := cfg.Title
	if title == "" {
		title = ev.Subject
	}
	sound := cfg.Sound
	if isOKEvent(ev.Type) && cfg.SoundOK != "" {
		sound = cfg.SoundOK
	}
	form := url.Values{}
	form.Set("token", cfg.AppToken)
	form.Set("user", cfg.UserKey)
	form.Set("message", ev.Body)
	form.Set("title", title)
	form.Set("priority", strconv.Itoa(cfg.Priority))
	form.Set("html", "1")
	if cfg.Device != "" {
		form.Set("device", cfg.Device)
	}
	if sound != "" {
		form.Set("sound", sound)
	}
	if cfg.TTL > 0 {
		form.Set("ttl", strconv.Itoa(cfg.TTL))
	}
	if cfg.Priority == 2 {
		retry, expire := cfg.Retry, cfg.Expire
		if retry < 30 {
			retry = 30
		}
		if expire <= 0 {
			expire = 3600
		}
		if expire > 86400 {
			expire = 86400
		}
		form.Set("retry", strconv.Itoa(retry))
		form.Set("expire", strconv.Itoa(expire))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.pushover.net/1/messages.json", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := s.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 300 {
		return fmt.Errorf("pushover status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

func isOKEvent(t string) bool {
	return t == EventJobSuccess || t == EventRestoreDone
}

func sanitizeHeader(s string) string {
	s = strings.ReplaceAll(s, "\r", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	return s
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
