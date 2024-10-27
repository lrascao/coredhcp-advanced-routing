package advrouting

import (
	"bytes"
	"context"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"time"

	"github.com/coredhcp/coredhcp/handler"
	"github.com/spf13/viper"
)

func setup(args0 ...string) (handler.Handler4, error) {
	args := strings.Join(args0, "\n")

	viper.SetConfigType("properties")
	viper.ReadConfig(bytes.NewBuffer([]byte(args)))

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("unable to unmarshal config: %w", err)
	}

	log.Infof("%s", config)

	ctx := context.Background()

	client, err := NewClient(ctx, config)
	if err != nil {
		return nil, err
	}

	var routers []*Router
	for _, r := range config.Routers {
		ip := net.ParseIP(r)
		if ip == nil {
			return nil, fmt.Errorf("invalid router IP: %s", r)
		}

		routers = append(routers, &Router{
			healthy: true,
			ip:      ip,
		})
	}

	p := PluginState{
		config:  config,
		client:  client,
		routers: routers,
	}

	// seed the random number generator
	rand.Seed(time.Now().UnixNano())

	go p.watchRouters(ctx)

	return p.Handler4, nil
}
