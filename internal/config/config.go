package config

import (
	"fmt"
	"os"
	"strconv"
	"time"
)

type Config struct {
	DatabaseURL, Port, LogLevel string
	WorkerInterval              time.Duration
}

func Load() (Config, error) {
	c := Config{
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		Port:           os.Getenv("PORT"),
		LogLevel:       os.Getenv("LOG_LEVEL"),
		WorkerInterval: 5 * time.Second,
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("DATABASE_URL is required")
	}
	if c.Port == "" {
		c.Port = "8080"
	}
	p, err := strconv.Atoi(c.Port)
	if err != nil || p < 1 || p > 65535 {
		return c, fmt.Errorf("PORT must be 1..65535")
	}
	if v := os.Getenv("WORKER_INTERVAL"); v != "" {
		c.WorkerInterval, err = time.ParseDuration(v)
		if err != nil || c.WorkerInterval < time.Second {
			return c, fmt.Errorf("WORKER_INTERVAL must be at least 1s")
		}
	}
	if c.LogLevel == "" {
		c.LogLevel = "INFO"
	}
	return c, nil
}
