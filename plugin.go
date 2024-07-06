package lbr

import (
	"math/rand"
	"net"
	"sync"
	"time"

	etcd "go.etcd.io/etcd/client/v3"

	"github.com/coredhcp/coredhcp/logger"
	"github.com/coredhcp/coredhcp/plugins"
	"github.com/insomniacslk/dhcp/dhcpv4"
)

// Plugin wraps plugin registration information
var Plugin = plugins.Plugin{
	Name:   "load-balanced-routers",
	Setup4: setup,
}

const (
	constDefaultSeparator = "::"
	constDefaultLeaseTime = 10 * time.Minute
)

type Router struct {
	ip        net.IP
	healthy   bool
	lastCheck time.Time
}

// PluginState is the data held by an instance of the range plugin
type PluginState struct {
	// Rough lock for the whole plugin, we'll get better performance once we use leasestorage
	sync.RWMutex
	config  Config
	client  *etcd.Client
	routers []*Router
}

// various global variables
var (
	log = logger.GetLogger("plugins/lbr")
)

// Handler4 handles DHCPv4 packets for the lbr plugin
func (p *PluginState) Handler4(req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
	p.RLock()
	defer p.RUnlock()

	// find the least loaded router
	var live []*Router
	for _, r := range p.routers {
		// ignore unhealthy routers
		if !r.healthy {
			continue
		}
		live = append(live, r)
	}

	if len(live) == 0 {
		log.Warnf("no healthy routers available")
		return nil, true
	}

	// do a random and pick a router out of the live ones
	router := live[rand.Int()%len(live)]

	resp.Options.Update(dhcpv4.OptRouter(router.ip))

	log.Debugf("router: %v", router.ip)

	return resp, false
}
