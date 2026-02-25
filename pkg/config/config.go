package config

import (
	"os"
	"strings"
	"time"
)

type Config struct {
	Token          string
	Users          []string
	Topic          string
	TargetDir      string
	Interval       time.Duration
	GlobalHooksDir string
	DryRun         bool
	SecretsDir     string // Directory to look for secret files
}

func LoadConfig() Config {
	interval, _ := time.ParseDuration(os.Getenv("SYNC_INTERVAL"))
	if interval == 0 {
		interval = 5 * time.Minute
	}

	usersStr := os.Getenv("GITHUB_USERS") // Expect comma-separated: "user1,org2,user3"
	users := strings.Split(usersStr, ",")
	for i := range users {
		users[i] = strings.TrimSpace(users[i])
	}

	return Config{
		Token:          os.Getenv("GITHUB_TOKEN"),
		Users:          users,
		Topic:          os.Getenv("TOPIC_FILTER"),
		TargetDir:      os.Getenv("TARGET_DIR"),
		Interval:       interval,
		DryRun:         os.Getenv("DRY_RUN") == "true",
		GlobalHooksDir: os.Getenv("GLOBAL_HOOKS_DIR"),
		SecretsDir:     os.Getenv("SECRETS_DIR"),
	}
}
