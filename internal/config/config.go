// Package config reads process environment variables. Docker Compose loads .env.
package config

import (
	"fmt"
	"math"
	"os"
	"strconv"
	"time"
)

type Config struct {
	HTTPAddr          string
	DataDir           string
	CatalogInterval   time.Duration
	RequestsPerSecond float64
}

func Load() (Config, error) {
	c := Config{HTTPAddr: value("HTTP_ADDR", ":8080"), DataDir: value("DATA_DIR", "./data")}
	minutes, err := strconv.ParseInt(value("CATALOG_REFRESH_MINUTES", "180"), 10, 64)
	if err != nil || minutes <= 0 || minutes > int64(time.Duration(1<<63-1)/time.Minute) {
		return c, fmt.Errorf("CATALOG_REFRESH_MINUTES must be a positive, valid number of minutes")
	}
	c.CatalogInterval = time.Duration(minutes) * time.Minute
	c.RequestsPerSecond, err = strconv.ParseFloat(value("WFM_REQUESTS_PER_SECOND", "2.5"), 64)
	if err != nil || math.IsNaN(c.RequestsPerSecond) || math.IsInf(c.RequestsPerSecond, 0) ||
		c.RequestsPerSecond <= 0 || c.RequestsPerSecond > 3 ||
		float64(time.Second)/c.RequestsPerSecond > float64(int64(1<<63-1)) {
		return c, fmt.Errorf("WFM_REQUESTS_PER_SECOND must be greater than 0 and no greater than 3")
	}
	return c, nil
}

func value(key, fallback string) string {
	if s := os.Getenv(key); s != "" {
		return s
	}
	return fallback
}
