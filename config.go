package lbr

import (
	"fmt"
	"time"
)

type Config struct {
	// etcd Config
	CA        string
	Cert      string
	Key       string
	Endpoints []string
	Prefix    string
	Routers   []string
	// health check
	HealthCheckDestination   string        `mapstructure:"health-check-destination"`
	HealthCheckInterval      time.Duration `mapstructure:"health-check-interval"`
	HealthCheckMaxPacketLoss int           `mapstructure:"health-check-max-packet-loss"`
	Interface                string        `mapstructure:"interface"`
}

func (c Config) String() string {
	return fmt.Sprintf("CA=%s Cert=%s Key=%s Endpoints=%v Routers=%v HealthCheckDestination=%s HealthCheckInterval=%v HealthCheckMaxPacketLoss=%d Interface=%s",
		c.CA, c.Cert, c.Key, c.Endpoints, c.Routers, c.HealthCheckDestination, c.HealthCheckInterval, c.HealthCheckMaxPacketLoss, c.Interface)
}
