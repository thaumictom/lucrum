package items

import (
	"errors"
	"fmt"
	"math"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type Config struct {
	FetchInterval     time.Duration
	DataDir           string
	RequestsPerSecond float64
	Debug             bool
}

func LoadConfig() (Config, error) {
	// Like dotenv in TypeScript: load local settings without replacing values
	// already supplied by Docker or the shell. A missing .env is fine.
	if err := godotenv.Load(); err != nil && !errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("load .env: %w", err)
	}

	config := Config{FetchInterval: 180 * time.Minute, DataDir: "./data", RequestsPerSecond: 2.5}
	if value, exists := os.LookupEnv("WFM_REQUESTS_PER_SECOND"); exists {
		rate, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		if err != nil || math.IsNaN(rate) || math.IsInf(rate, 0) || rate <= 0 || rate > 3 {
			return Config{}, errors.New("WFM_REQUESTS_PER_SECOND must be greater than 0 and at most 3")
		}
		config.RequestsPerSecond = rate
	}
	if value, exists := os.LookupEnv("DEBUG"); exists {
		debug, err := strconv.ParseBool(strings.TrimSpace(value))
		if err != nil {
			return Config{}, errors.New("DEBUG must be a boolean (true or false)")
		}
		config.Debug = debug
	}
	if value, exists := os.LookupEnv("FETCH_INTERVAL_MINUTES"); exists {
		minutes, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		// A Duration counts nanoseconds in an int64, so check before multiplying.
		const maxMinutes = (1<<63 - 1) / int64(time.Minute)
		if err != nil || minutes <= 0 || minutes > maxMinutes {
			return Config{}, fmt.Errorf("FETCH_INTERVAL_MINUTES must be a positive whole number no greater than %d", maxMinutes)
		}
		config.FetchInterval = time.Duration(minutes) * time.Minute
	}
	if value, exists := os.LookupEnv("DATA_DIR"); exists {
		config.DataDir = strings.TrimSpace(value)
		if config.DataDir == "" {
			return Config{}, errors.New("DATA_DIR must not be empty")
		}
	}
	return config, nil
}
