package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	DB       DBConfig
	OpenAI   OpenAIConfig
	Server   ServerConfig
}

type DBConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	Name     string
}

func (d DBConfig) DSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable TimeZone=Asia/Shanghai",
		d.Host, d.Port, d.User, d.Password, d.Name,
	)
}

type OpenAIConfig struct {
	APIKey   string
	BaseURL  string
	Model    string
}

type ServerConfig struct {
	Port int
}

func Load() *Config {
	return &Config{
		DB: DBConfig{
			Host:     getEnv("DB_HOST", "localhost"),
			Port:     getEnvInt("DB_PORT", 5432),
			User:     getEnv("DB_USER", "postgres"),
			Password: getEnv("DB_PASSWORD", "postgres"),
			Name:     getEnv("DB_NAME", "note_memory"),
		},
		OpenAI: OpenAIConfig{
			APIKey:  getEnv("OPENAI_API_KEY", ""),
			BaseURL: getEnv("OPENAI_BASE_URL", "https://api.openai.com/v1"),
			Model:   getEnv("OPENAI_MODEL", "gpt-4o-mini"),
		},
		Server: ServerConfig{
			Port: getEnvInt("SERVER_PORT", 8080),
		},
	}
}

func getEnv(key, defaultVal string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return defaultVal
}
