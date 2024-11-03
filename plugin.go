package advrouting

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	etcd "go.etcd.io/etcd/client/v3"
	etcdutil "go.etcd.io/etcd/client/v3/clientv3util"

	"github.com/coredhcp/coredhcp/logger"
	"github.com/coredhcp/coredhcp/plugins"
	"github.com/insomniacslk/dhcp/dhcpv4"
)

// Plugin wraps plugin registration information
var Plugin = plugins.Plugin{
	Name:   "advanced-routing",
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
	log = logger.GetLogger("plugins/advrouting")
)

// Handler4 handles DHCPv4 packets for the plugin
func (p *PluginState) Handler4(req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
	p.RLock()
	defer p.RUnlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

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
		log.Errorf("no healthy routers available")
		return nil, true
	}

	// pick out a random router out of healthy ones
	router := random(routers)

	lease, err := etcd.NewLease(p.client).
		Grant(ctx, int64(30))
	if err != nil {
		log.Errorf("could not create new lease: %v", err)
		return nil, true
	}

	nicTransactionKey := fmt.Sprintf("%s/nics/transactions/%s/%s",
		p.config.Prefix, req.ClientHWAddr.String(), req.TransactionID)

	kvc := etcd.NewKV(p.client)

	_, err = kvc.Txn(ctx).If(
		etcdutil.KeyMissing(nicTransactionKey),
	).Then(
		etcd.OpTxn([]etcd.Cmp{
			etcdutil.KeyMissing(nicTransactionKey),
		}, []etcd.Op{
			etcd.OpPut(nicTransactionKey, router.String(), etcd.WithLease(lease.ID)),
		}, nil),
	).Commit()
	if err != nil {
		log.Errorf("could not commit nic transaction: %v", err)
	}

	// either way, a router for this nic transaction now exists
	// so we can return the router
	res, err := kvc.Get(ctx, nicTransactionKey)
	if err != nil {
		log.Errorf("could not get router for nic transaction: %v", err)
		return nil, true
	}
	if res.Count == 0 {
		log.Errorf("no router found for nic transaction")
		return nil, true
	}

	router = net.ParseIP(string(res.Kvs[0].Value))
	if router == nil {
		log.Errorf("could not parse router IP obtained etcd: %v", res.Kvs[0].Value)
		return nil, true

	}

	log.Infof("setting router in DHCPv4 response (txid: %v): %v",
		resp.TransactionID, router)

	resp.Options.Update(dhcpv4.OptRouter(router))

	return resp, false
}

func random(ips []net.IP) net.IP {
	return ips[rand.Intn(len(ips))]
}
