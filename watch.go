package advrouting

import (
	"context"
	"fmt"
	"net"
	"time"

	probing "github.com/prometheus-community/pro-bing"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

func (p *PluginState) watchRouters(ctx context.Context) {
	ticker := time.NewTicker(p.config.HealthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// find the oldest router
			var oldest *Router
			for _, r := range p.routers {
				if oldest == nil || r.lastCheck.Before(oldest.lastCheck) {
					oldest = r
				}
			}

			// check its health
			if err := p.checkRouter(ctx, oldest); err != nil {
				log.Errorf("error checking router %v: %v", oldest.ip, err)
			}
		}
	}
}

func (p *PluginState) checkRouter(ctx context.Context, r *Router) error {
	log.Debugf("checking router %v (healthy?: %v)",
		r.ip, r.healthy)
	r.lastCheck = time.Now()

	// change the default route to this router
	if err := p.changeDefaultRoute(r.ip.String()); err != nil {
		log.Warnf("error changing default route to destination: %v", err)
		return fmt.Errorf("error changing default route: %w", err)
	}

	ctx, cancel := context.WithTimeout(ctx, time.Duration(15*time.Second))
	defer cancel()

	// ping the health check destination
	pinger, err := probing.NewPinger(p.config.HealthCheckDestination)
	if err != nil {
		return fmt.Errorf("error creating pinger: %w", err)
	}
	pinger.Count = 10
	pinger.Interval = time.Duration(500 * time.Millisecond)
	pinger.Timeout = time.Duration(10 * time.Second)
	if err := pinger.RunWithContext(ctx); err != nil {
		return fmt.Errorf("error running pinger: %w", err)
	}
	stats := pinger.Statistics()
	if stats.PacketLoss > float64(p.config.HealthCheckMaxPacketLoss) {
		log.Warnf("router %v is unhealthy, %v%% packet loss exceeded max (%v%%)",
			r.ip, stats.PacketLoss, p.config.HealthCheckMaxPacketLoss)

		p.Lock()
		defer p.Unlock()

		// mark router as unhealthy
		r.healthy = false
	} else if !r.healthy {
		log.Infof("router %v is healthy again", r.ip)

		p.Lock()
		defer p.Unlock()

		// mark router as healthy
		r.healthy = true
	}

	return nil
}

func (p *PluginState) changeDefaultRoute(gw string) error {
	// get interface
	link, err := netlink.LinkByName(p.config.Interface)
	if err != nil {
		return fmt.Errorf("error finding link: %w", err)
	}

	// get routes
	routes, err := netlink.RouteList(link, unix.AF_INET)
	if err != nil {
		return fmt.Errorf("error listing routes: %w", err)
	}

	ipgw := net.ParseIP(gw)

	// find the default route and change it's gateway
	for _, route := range routes {
		if route.Dst != nil && route.Dst.IP.String() == p.config.HealthCheckDestination {
			route.Gw = ipgw
			if err := netlink.RouteChange(&route); err != nil {
				return fmt.Errorf("error changing route: %w", err)
			}

			return nil
		}
	}

	// default route not found, create it
	dest := net.ParseIP(p.config.HealthCheckDestination)
	if err := netlink.RouteAdd(&netlink.Route{
		LinkIndex: link.Attrs().Index,
		Dst: &net.IPNet{
			IP:   dest,
			Mask: net.CIDRMask(32, 32),
		},
		Gw: ipgw,
	}); err != nil {
		return fmt.Errorf("error adding route: %w", err)
	}

	return nil
}
