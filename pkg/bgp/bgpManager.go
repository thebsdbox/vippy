package bgp

import (
	"context"
	"fmt"
	"time"

	api "github.com/osrg/gobgp/api"
	gobgp "github.com/osrg/gobgp/pkg/server"
	log "github.com/sirupsen/logrus"
)

// BGPManager holds the configuration for all the BGP information
type BGPManager struct {
	s *gobgp.BgpServer
	c *Config
}

type Peer struct {
	Address string
	AS      uint32
}

type Config struct {
	AS       uint32
	RouterID string
	NextHop  string
	SourceIP string
	SourceIF string

	Peers []Peer
	IPv6  bool
}

func NewBgp(c *Config) (b *BGPManager, err error) {
	if c.AS == 0 {
		return nil, fmt.Errorf("You need to provide AS")
	}

	if c.SourceIP != "" && c.SourceIF != "" {
		return nil, fmt.Errorf("SourceIP and SourceIF are mutually exclusive")
	}

	if len(c.Peers) == 0 {
		return nil, fmt.Errorf("You need to provide at least one peer")
	}

	b = &BGPManager{
		s: gobgp.NewBgpServer(),
		c: c,
	}
	go b.s.Serve()

	if err = b.s.StartBgp(context.Background(), &api.StartBgpRequest{
		Global: &api.Global{
			As:         c.AS,
			RouterId:   c.RouterID,
			ListenPort: -1,
		},
	}); err != nil {
		return
	}

	if err = b.s.MonitorPeer(context.Background(), &api.MonitorPeerRequest{}, func(p *api.Peer) { log.Println(p) }); err != nil {
		return
	}

	for _, p := range c.Peers {
		if err = b.AddPeer(p); err != nil {
			return
		}
	}

	return
}

func (b *BGPManager) Close() error {
	ctx, cf := context.WithTimeout(context.Background(), 5*time.Second)
	defer cf()
	return b.s.StopBgp(ctx, &api.StopBgpRequest{})
}
