package bgp

import (
	"context"
	"fmt"
	"net"
	"strconv"
	"strings"

	api "github.com/osrg/gobgp/api"

	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/any"
)

func (b *BGPManager) AddPeer(peer Peer) (err error) {
	port := 179

	if t := strings.SplitN(peer.Address, ":", 2); len(t) == 2 {
		peer.Address = t[0]

		if port, err = strconv.Atoi(t[1]); err != nil {
			return fmt.Errorf("Unable to parse port '%s' as int: %s", t[1], err)
		}
	}

	p := &api.Peer{
		Conf: &api.PeerConf{
			NeighborAddress: peer.Address,
			PeerAs:          peer.AS,
		},

		Timers: &api.Timers{
			Config: &api.TimersConfig{
				ConnectRetry: 10,
			},
		},

		Transport: &api.Transport{
			MtuDiscovery:  true,
			RemoteAddress: peer.Address,
			RemotePort:    uint32(port),
		},
	}

	if b.c.SourceIP != "" {
		p.Transport.LocalAddress = b.c.SourceIP
	}
	if b.c.SourceIF != "" {
		p.Transport.BindInterface = b.c.SourceIF
	}

	return b.s.AddPeer(context.Background(), &api.AddPeerRequest{
		Peer: p,
	})
}

func (b *BGPManager) getPath(ip net.IP) *api.Path {
	var pfxLen uint32 = 32
	if ip.To4() == nil {
		if !b.c.IPv6 {
			return nil
		}

		pfxLen = 128
	}

	nlri, _ := ptypes.MarshalAny(&api.IPAddressPrefix{
		Prefix:    ip.String(),
		PrefixLen: pfxLen,
	})

	a1, _ := ptypes.MarshalAny(&api.OriginAttribute{
		Origin: 0,
	})

	var nh string
	if b.c.NextHop != "" {
		nh = b.c.NextHop
	} else if b.c.SourceIP != "" {
		nh = b.c.SourceIP
	} else {
		nh = b.c.RouterID
	}

	a2, _ := ptypes.MarshalAny(&api.NextHopAttribute{
		NextHop: nh,
	})

	return &api.Path{
		Family: &api.Family{
			Afi:  api.Family_AFI_IP,
			Safi: api.Family_SAFI_UNICAST,
		},
		Nlri:   nlri,
		Pattrs: []*any.Any{a1, a2},
	}
}

func (b *BGPManager) AddHost(addr string) (err error) {
	ip, _, err := net.ParseCIDR(addr)
	if err != nil {
		return err
	}
	p := b.getPath(ip)
	if p == nil {
		return
	}

	_, err = b.s.AddPath(context.Background(), &api.AddPathRequest{
		Path: p,
	})

	return
}

func (b *BGPManager) DelHost(addr string) (err error) {
	ip, _, err := net.ParseCIDR(addr)
	if err != nil {
		return err
	}
	p := b.getPath(ip)
	if p == nil {
		return
	}

	return b.s.DeletePath(context.Background(), &api.DeletePathRequest{
		Path: p,
	})
}
