package lbr

import (
	"fmt"
	"time"
)

type Config struct {
	CA        string
	Cert      string
	Key       string
	Endpoints []string
	Routers   []string
	// health check
	HealthCheckInterval time.Duration `mapstructure:"health-check-interval"`
	Interface           string        `mapstructure:"interface"`
}

func (c Config) String() string {
	return fmt.Sprintf("CA=%s Cert=%s Key=%s Endpoints=%v Routers=%v",
		c.CA, c.Cert, c.Key, c.Endpoints, c.Routers)
}
