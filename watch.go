package lbr

import (
	"context"
	"fmt"
	"net"
	"time"

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

			// check it's health
			p.checkRouter(oldest)
		}
	}
}

func (p *PluginState) checkRouter(r *Router) {
	p.Lock()
	defer p.Unlock()

	log.Debugf("checking router %v", r.ip)
	r.lastCheck = time.Now()

	// change the default route to this router
	if err := p.changeDefaultRoute(r.ip.String()); err != nil {
		log.Warnf("error changing default route: %v", err)
		return
	}
}

func (p *PluginState) changeDefaultRoute(gw string) error {
	// get interface
	link, err := netlink.LinkByName(p.config.Interface)
	if err != nil {
		return fmt.Errorf("error finding link: %w", err)
	}

	routes, err := netlink.RouteList(link, unix.AF_INET)
	if err != nil {
		return fmt.Errorf("error listing routes: %w", err)
	}

	// find the default route
	for _, route := range routes {
		if route.Dst != nil && route.Dst.String() == "0.0.0.0/0" {
			route.Gw = net.ParseIP(gw)
			if err := netlink.RouteChange(&route); err != nil {
				return fmt.Errorf("error changing route: %w", err)
			}

			return nil
		}
	}

	return fmt.Errorf("default route not found")
}
