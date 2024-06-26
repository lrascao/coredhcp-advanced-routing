package lbr

import (
	"fmt"
	"time"
)

type Config struct {
	Routers []string
	// health check
	HealthCheckInterval time.Duration `mapstructure:"health-check-interval"`
	Interface           string        `mapstructure:"interface"`
}

func (c Config) String() string {
	return fmt.Sprintf("Routers=%v HealthCheckInterval=%v Interface=%v",
		c.Routers, c.HealthCheckInterval, c.Interface)
}
