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

const (
	constDefaultLeaseTime = 10 * time.Minute
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

	log.Infof("setting router in DHCPv4 response (%s txid: %v): %v",
		resp.MessageType(), resp.TransactionID, router)

	resp.Options.Update(dhcpv4.OptRouter(router))

	leaseTime := resp.IPAddressLeaseTime(constDefaultLeaseTime)
	lease, err := etcd.NewLease(p.client).
		Grant(ctx, int64(leaseTime.Seconds()))
	if err != nil {
		log.Errorf("could not create new lease (ttl: %v): %v",
			leaseTime, err)
		return nil, true
	}

	routerTransactionKey := fmt.Sprintf("%s/routers/%s/%s",
		p.config.Prefix, router.String(), resp.YourIPAddr.String())

	kvc := etcd.NewKV(p.client)

	_, err = kvc.Txn(ctx).If(
		etcdutil.KeyMissing(routerTransactionKey),
	).Then(
		etcd.OpTxn(
			[]etcd.Cmp{
				etcdutil.KeyMissing(routerTransactionKey),
			},
			[]etcd.Op{
				etcd.OpPut(routerTransactionKey, req.ClientHWAddr.String(), etcd.WithLease(lease.ID)),
			}, nil),
	).Commit()
	if err != nil {
		log.Errorf("could not commit nic router: %v", err)
	}

	return resp, false
}

func random(ips []net.IP) net.IP {
	return ips[rand.Intn(len(ips))]
}
