package lbr

import (
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

	// return all live routers
	var routers []net.IP
	for _, r := range p.routers {
		// ignore unhealthy routers
		if !r.healthy {
			continue
		}
		routers = append(routers, r.ip)
	}

	if len(routers) == 0 {
		log.Warnf("no healthy routers available")
		return nil, true
	}

	routers = sort(routers)
	log.Infof("setting routers in DHCPv4 response (txid: %v): %v",
		resp.TransactionID, routers)

	resp.Options.Update(dhcpv4.OptRouter(routers...))

	return resp, false
}

// sort sorts the given slice of net.IP addresses in a stable way
func sort(s []net.IP) []net.IP {
	// sort the slice
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			if s[i].String() > s[j].String() {
				s[i], s[j] = s[j], s[i]
			}
		}
	}
	return s
}
