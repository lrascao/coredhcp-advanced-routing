package advrouting

import (
	"context"
	"fmt"
	"math/rand"
	"net"
	"sync"
	"time"

	etcd "go.etcd.io/etcd/client/v3"

	"github.com/coredhcp/coredhcp/logger"
	"github.com/coredhcp/coredhcp/plugins"
	"github.com/insomniacslk/dhcp/dhcpv4"
)

const (
	constDefaultLeaseTime = 10 * time.Minute
	constShortLeaseTime   = 1 * time.Minute
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

const (
	routersPrefix = "routers"
)

// Handler4 handles DHCPv4 packets for the plugin
func (p *PluginState) Handler4(req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// an INFORM carries no YourIPAddr, the client already owns its address
	// (ciaddr); answer read-only from any stored association
	if req.MessageType() == dhcpv4.MessageTypeInform &&
		!req.ClientIPAddr.IsUnspecified() {
		return p.handleInform(ctx, req, resp)
	}

	// we need an IP address to go on
	if resp.YourIPAddr.IsUnspecified() {
		log.Errorf("no IP address in DHCPv4 response (%s txid: %v)",
			resp.MessageType(), resp.TransactionID)
		return resp, false
	}

	routers, err := p.healthyRouters()
	if err != nil {
		// There is nothing to advertise, but the rest of the response is
		// still good: leave whatever the `router` plugin set earlier in the
		// chain and let the reply go out. Returning nil here would have the
		// server drop the request entirely.
		log.Errorf("could not get healthy routers, leaving the default router in place: %v", err)
		return resp, false
	}

	kvc := etcd.NewKV(p.client)

	ipKey := p.ipKey(resp.YourIPAddr)

	// is there already a router associated with this IP?
	ipResp, err := kvc.Get(ctx, ipKey)
	if err != nil {
		router := random(routers)

		log.Errorf("could not get router for ip %s, proceeding anyway with router %s: %v",
			resp.YourIPAddr, router, err)

		resp.Options.Update(dhcpv4.OptRouter(router))

		return resp, false
	}

	var (
		oldRouter, router net.IP
		observedRev       int64
		// lease currently holding this association, captured so it can be
		// revoked once the transaction moves the keys onto a fresh lease
		oldLease etcd.LeaseID
	)
	if len(ipResp.Kvs) == 1 {
		router = net.ParseIP(string(ipResp.Kvs[0].Value))
		observedRev = ipResp.Kvs[0].ModRevision
		oldLease = etcd.LeaseID(ipResp.Kvs[0].Lease)

		log.Debugf("found previously allocated router in DHCPv4 response (%s txid: %v): %v, IP: %s (mod revision: %v, lease: %v)",
			resp.MessageType(), resp.TransactionID, router, resp.YourIPAddr, observedRev, oldLease)
	}

	// if the response is not an ACK or OFFER, we either return the previously allocated router or send empty router
	// if none allocated yet.
	if resp.MessageType() != dhcpv4.MessageTypeAck &&
		resp.MessageType() != dhcpv4.MessageTypeOffer {
		log.Infof("setting previously allocated router in DHCPv4 response (%s txid: %v): %v, IP: %s",
			resp.MessageType(), resp.TransactionID, router, resp.YourIPAddr)

		if router != nil {
			resp.Options.Update(dhcpv4.OptRouter(router))
		}

		return resp, false
	}

	// no router yet, pick a random one from the healthy ones
	if router == nil {
		router = random(routers)

		log.Infof("allocated router in DHCPv4 response (%s txid: %v): %v, IP: %s",
			resp.MessageType(), resp.TransactionID, router, resp.YourIPAddr)
	} else {
		// is the router still healthy? If not, pick a new one
		if !contains(routers, router) {
			oldRouter = router
			router = random(routers)

			log.Infof("router %v was found to be unhealthy, allocated new router in DHCPv4 response (%s txid: %v): %v, IP: %s",
				oldRouter, resp.MessageType(), resp.TransactionID, router, resp.YourIPAddr)
		} else {
			log.Infof("found previously allocated router in DHCPv4 response (%s txid: %v): %v, IP: %s",
				resp.MessageType(), resp.TransactionID, router, resp.YourIPAddr)
		}
	}

	leaseTime := resp.IPAddressLeaseTime(constDefaultLeaseTime)
	if resp.MessageType() == dhcpv4.MessageTypeOffer && observedRev == 0 {
		log.Debugf("first allocation for IP %s, setting short lease time for OFFER",
			resp.YourIPAddr)
		// set short leases for OFFERs on the first allocation
		leaseTime = constShortLeaseTime
	}

	lease, err := etcd.NewLease(p.client).
		Grant(ctx, int64(leaseTime.Seconds()))
	if err != nil {
		log.Errorf("could not create new lease (ttl: %v): %v",
			leaseTime, err)

		resp.Options.Update(dhcpv4.OptRouter(router))

		return resp, false
	}

	routerTransactionKey := fmt.Sprintf("%s/%s/%s/%s",
		p.config.Prefix, routersPrefix, router.String(), resp.YourIPAddr.String())

	putOpts := []etcd.Op{
		etcd.OpPut(ipKey, router.String(), etcd.WithLease(lease.ID)),
		etcd.OpPut(routerTransactionKey, req.ClientHWAddr.String(), etcd.WithLease(lease.ID)),
	}

	// this means the previously allocated router is unhealthy, so we need to delete the old transaction key
	if oldRouter != nil {
		oldRouterTransactionKey := fmt.Sprintf("%s/%s/%s/%s",
			p.config.Prefix, routersPrefix, oldRouter.String(), resp.YourIPAddr.String())

		putOpts = append(putOpts, etcd.OpDelete(oldRouterTransactionKey))
	}

	txnResp, err := kvc.Txn(ctx).If(
		etcd.Compare(etcd.ModRevision(ipKey), "=", observedRev),
	).Then(putOpts...).Else(
		etcd.OpGet(ipKey),
	).Commit()
	if err != nil {
		log.Errorf("could not commit router: %v", err)

		resp.Options.Update(dhcpv4.OptRouter(router))

		return resp, false
	}

	if !txnResp.Succeeded {
		log.Infof("IP %s has a different router associated with it than expected, using that instead",
			resp.YourIPAddr)

		if _, err := etcd.NewLease(p.client).Revoke(ctx, lease.ID); err != nil {
			log.Errorf("could not revoke unused lease %v: %v", lease.ID, err)
		}

		if len(txnResp.Responses) == 0 || len(txnResp.Responses[0].GetResponseRange().Kvs) == 0 {
			log.Errorf("Unable to get router for IP %s, no value returned from etcd",
				resp.YourIPAddr)

			resp.Options.Update(dhcpv4.OptRouter(router))

			return resp, false
		}

		if r := net.ParseIP(string(txnResp.Responses[0].GetResponseRange().Kvs[0].Value)); r != nil {
			router = r
		}
	} else if oldLease != 0 {
		// the previous association's keys have moved onto the fresh lease,
		// draining the old one; revoke it so it does not linger until its TTL
		if _, err := etcd.NewLease(p.client).Revoke(ctx, oldLease); err != nil {
			log.Errorf("could not revoke superseded lease %v: %v", oldLease, err)
		}
	}

	resp.Options.Update(dhcpv4.OptRouter(router))

	return resp, false
}

// handleInform answers an INFORM read-only: if the client's address has a
// router associated and that router is healthy, set it in the ACK, otherwise
// leave the option out. No state is created or refreshed either way, RFC 2131
// tells servers not to keep lease state for INFORM clients.
func (p *PluginState) handleInform(ctx context.Context, req, resp *dhcpv4.DHCPv4) (*dhcpv4.DHCPv4, bool) {
	ipResp, err := etcd.NewKV(p.client).Get(ctx, p.ipKey(req.ClientIPAddr))
	if err != nil {
		log.Errorf("could not get router for inform ip %s: %v", req.ClientIPAddr, err)
		return resp, false
	}
	if len(ipResp.Kvs) != 1 {
		log.Debugf("no router associated with inform ip %s", req.ClientIPAddr)
		return resp, false
	}

	router := net.ParseIP(string(ipResp.Kvs[0].Value))
	if router == nil {
		log.Errorf("could not parse router IP obtained from etcd: %s", ipResp.Kvs[0].Value)
		return resp, false
	}

	routers, err := p.healthyRouters()
	if err != nil || !contains(routers, router) {
		// a gateway known to be down is worse than none at all
		log.Infof("router %v associated with inform ip %s is not healthy, omitting router option",
			router, req.ClientIPAddr)
		return resp, false
	}

	log.Infof("setting stored router in INFORM ACK (txid: %v): %v, IP: %s",
		resp.TransactionID, router, req.ClientIPAddr)

	resp.Options.Update(dhcpv4.OptRouter(router))

	return resp, false
}

func (p *PluginState) ipKey(ip net.IP) string {
	return fmt.Sprintf("%s/%s/ips/%s", p.config.Prefix, routersPrefix, ip.String())
}

// healthyRouters returns the routers currently eligible to be handed to a
// client.
//
// A unanimous failure is evidence about the probe, not about the routers. All
// of them are checked against the same health-check-destination, reached over
// the same uplink, so anything wrong with that destination fails every router
// in the same sweep while every one of them is still forwarding perfectly
// well. When that happens this returns the full configured set rather than
// nothing, and says so at ERROR.
//
// It used to return an error instead, which made Handler4 drop the request:
// one bad probe destination stopped DHCP for the entire network, and clients
// lost addressing on top of whatever the original fault was. A client pointed
// at a gateway that might be down can still recover -- it has an address, and
// the next sweep to succeed will move it. A client with no lease at all cannot.
//
// Only a unanimous failure is treated this way. Partial pruning (3 healthy ->
// 2) is real information, since the surviving routers prove the probe itself
// works, and is left alone.
func (p *PluginState) healthyRouters() ([]net.IP, error) {
	p.RLock()
	defer p.RUnlock()

	// nothing configured is an operator error, not a health verdict, and
	// there is no sensible set to fall back to
	if len(p.routers) == 0 {
		return nil, fmt.Errorf("no routers configured")
	}

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
		for _, r := range p.routers {
			routers = append(routers, r.ip)
		}

		log.Errorf("no router passed its health check against %s: treating a unanimous failure as a fault in the probe and offering all %d configured routers",
			p.config.HealthCheckDestination, len(routers))
	}

	return routers, nil
}

func random(ips []net.IP) net.IP {
	return ips[rand.Intn(len(ips))]
}

func contains(ips []net.IP, ip net.IP) bool {
	for _, i := range ips {
		if i.Equal(ip) {
			return true
		}
	}
	return false
}
