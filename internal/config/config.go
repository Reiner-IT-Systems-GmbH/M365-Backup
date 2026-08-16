package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"

	"github.com/rhw/m365backup/internal/db"
)

type Config struct {
	HTTPAddr          string
	PublicBaseURL     string
	DBDriver          db.Driver
	DatabasePath      string
	MySQLDSN          string
	KopiaRoot         string
	StagingRoot       string
	MasterKey         string
	AdminUser         string
	AdminPassword     string
	MaxConcurrentJobs int
	ExchangeWorkers   int
	// DisplayTZ is the IANA zone for UI timestamps (storage stays UTC).
	DisplayTZ    string
	DisplayLoc   *time.Location
	SMTPHost     string
	SMTPPort     int
	SMTPUsername string
	SMTPPassword string
	SMTPFrom     string
	SMTPTo       []string
}

func Load() (*Config, error) {
	driver := db.Driver(strings.ToLower(getenv("DB_DRIVER", "sqlite")))
	cfg := &Config{
		HTTPAddr:          getenv("HTTP_ADDR", ":8080"),
		PublicBaseURL:     strings.TrimRight(getenv("PUBLIC_BASE_URL", "http://localhost:8080"), "/"),
		DBDriver:          driver,
		DatabasePath:      getenv("DATABASE_PATH", "./data/m365backup.db"),
		MySQLDSN:          os.Getenv("MYSQL_DSN"),
		KopiaRoot:         getenv("KOPIA_ROOT", "./data/kopia"),
		StagingRoot:       getenv("STAGING_ROOT", "./data/staging"),
		MasterKey:         os.Getenv("MASTER_KEY"),
		AdminUser:         getenv("ADMIN_USER", "m365adminuser"),
		AdminPassword:     os.Getenv("ADMIN_PASSWORD"),
		MaxConcurrentJobs: getenvInt("MAX_CONCURRENT_JOBS", 2),
		ExchangeWorkers:   getenvInt("EXCHANGE_WORKERS", 6),
		DisplayTZ:         displayTZName(),
		SMTPHost:          os.Getenv("SMTP_HOST"),
		SMTPPort:          getenvInt("SMTP_PORT", 587),
		SMTPUsername:      os.Getenv("SMTP_USERNAME"),
		SMTPPassword:      os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:          os.Getenv("SMTP_FROM"),
	}
	if to := os.Getenv("SMTP_TO"); to != "" {
		cfg.SMTPTo = splitComma(to)
	}

	loc, err := time.LoadLocation(cfg.DisplayTZ)
	if err != nil {
		return nil, fmt.Errorf("DISPLAY_TZ %q: %w", cfg.DisplayTZ, err)
	}
	cfg.DisplayLoc = loc

	if cfg.MasterKey == "" {
		return nil, fmt.Errorf("MASTER_KEY is required (openssl rand -base64 32)")
	}
	cfg.AdminUser = normalizeAdminUser(cfg.AdminUser)
	if cfg.AdminUser == "" {
		return nil, fmt.Errorf("ADMIN_USER is invalid (use letters, digits, _ or -, 3–64 chars)")
	}
	if len(cfg.AdminPassword) < 8 {
		return nil, fmt.Errorf("ADMIN_PASSWORD is required (min 8 characters)")
	}
	if cfg.MaxConcurrentJobs < 1 {
		cfg.MaxConcurrentJobs = 1
	}
	if cfg.ExchangeWorkers < 1 {
		cfg.ExchangeWorkers = 6
	}
	if cfg.ExchangeWorkers > 32 {
		cfg.ExchangeWorkers = 32
	}

	if cfg.DBDriver == db.DriverMySQL || cfg.DBDriver == "mariadb" {
		cfg.DBDriver = db.DriverMySQL
		if cfg.MySQLDSN == "" {
			cfg.MySQLDSN = buildMySQLDSN()
		}
		if cfg.MySQLDSN == "" {
			return nil, fmt.Errorf("for DB_DRIVER=mysql set MYSQL_DSN or MYSQL_USER/MYSQL_PASSWORD/MYSQL_HOST/MYSQL_DATABASE")
		}
	}

	return cfg, nil
}

func (c *Config) DBOptions() db.Options {
	return db.Options{
		Driver:     c.DBDriver,
		SQLitePath: c.DatabasePath,
		MySQLDSN:   c.MySQLDSN,
	}
}

func buildMySQLDSN() string {
	user := os.Getenv("MYSQL_USER")
	pass := os.Getenv("MYSQL_PASSWORD")
	host := getenv("MYSQL_HOST", "127.0.0.1")
	port := getenv("MYSQL_PORT", "3306")
	name := os.Getenv("MYSQL_DATABASE")
	if user == "" || name == "" {
		return ""
	}
	mc := mysqldriver.NewConfig()
	mc.User = user
	mc.Passwd = pass
	mc.Net = "tcp"
	mc.Addr = host + ":" + port
	mc.DBName = name
	mc.ParseTime = true
	mc.Loc = time.UTC
	mc.Params = map[string]string{"charset": "utf8mb4"}
	return mc.FormatDSN()
}

func normalizeAdminUser(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if len(s) < 3 || len(s) > 64 {
		return ""
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return ""
	}
	return s
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

// displayTZName: DISPLAY_TZ, else TZ, else Europe/Berlin (German UI default).
func displayTZName() string {
	if v := os.Getenv("DISPLAY_TZ"); v != "" {
		return v
	}
	if v := os.Getenv("TZ"); v != "" {
		return v
	}
	return "Europe/Berlin"
}

// FormatTime renders t in the configured display zone (empty if zero).
func (c *Config) FormatTime(t time.Time, layout string) string {
	if t.IsZero() {
		return ""
	}
	loc := c.DisplayLoc
	if loc == nil {
		loc = time.UTC
	}
	return t.In(loc).Format(layout)
}

func getenvInt(k string, def int) int {
	v := os.Getenv(k)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func splitComma(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
