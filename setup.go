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
	"github.com/go-viper/encoding/javaproperties"
	"github.com/spf13/viper"
)

func setup(args0 ...string) (handler.Handler4, error) {
	args := strings.Join(args0, "\n")

	codecRegistry := viper.NewCodecRegistry()
	codec := &javaproperties.Codec{}
	codecRegistry.RegisterCodec("properties", codec)

	v := viper.NewWithOptions(
		viper.WithCodecRegistry(codecRegistry),
	)
	v.SetConfigType("properties")
	if err := v.ReadConfig(bytes.NewBuffer([]byte(args))); err != nil {
		return nil, fmt.Errorf("unable to read config: %w", err)
	}

	var config Config
	if err := v.Unmarshal(&config); err != nil {
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
			// all routers are initially healthy
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
