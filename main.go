package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cloudflare/ipvs"
	"golang.org/x/sys/unix"

	"github.com/golang/protobuf/ptypes"
	"github.com/golang/protobuf/ptypes/any"
	"github.com/google/uuid"
	"github.com/gorilla/mux"
	api "github.com/osrg/gobgp/api"
	gobgp "github.com/osrg/gobgp/pkg/server"
	"github.com/packethost/packngo"
	"github.com/pkg/errors"
	log "github.com/sirupsen/logrus"
	"github.com/vishvananda/netlink"
)

// curl -X POST http://code:10001/loadbalancer -H 'Content-Type: application/json' -d '{"name":"test loadbalancer", "port": "6443"}'
// curl -X POST http://code:10001/loadbalancer/uuid/backend -H 'Content-Type: application/json' -d '{"ip":"1.1.1.1", "port": "6443"}'

// curl -X POST http://code:10001/loadbalancer/02b5d4d9-f40b-47a1-aa74-91631732bab1/backend -H 'Content-Type: application/json' -d '{"ip":"1.1.1.1", "port": "6443"}'


var projectID, facility string
var bgpConfig *BGPConfig
var emClient *packngo.Client

type BGPConfig struct {
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

type LoadBalancer struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`

	EIP  string `json:"eip"`
	Port int    `json:"port"`

	ipvs    *IPVSLoadBalancer
	Backend []Backends `json:"backends"`
}

type Backends struct {
	UUID string `json:"uuid"`
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

var LoadBalancers []LoadBalancer

func handleRequests() {
	// creates a new instance of a mux router
	myRouter := mux.NewRouter().StrictSlash(true)
	// replace http.HandleFunc with myRouter.HandleFunc

	myRouter.HandleFunc("/loadbalancer", createNewLoadBalancer).Methods("POST")
	myRouter.HandleFunc("/loadbalancers", returnAllLoadBalancers)
	myRouter.HandleFunc("/loadbalancer/{uuid}", returnSingleLoadBalancer).Methods("GET")
	myRouter.HandleFunc("/loadbalancer/{uuid}", deleteLoadBalancer).Methods("DELETE")
	myRouter.HandleFunc("/loadbalancer/{uuid}/backend", createNewBackend).Methods("POST")
	myRouter.HandleFunc("/loadbalancer/{uuid}/backend/{backenduuid}", deleteLoadBalancer).Methods("DELETE")

	myRouter.HandleFunc("/", homePage)

	// finally, instead of passing in nil, we want
	// to pass in our newly created router as the second
	// argument
	log.Fatal(http.ListenAndServe(":10001", myRouter))
}

func createNewLoadBalancer(w http.ResponseWriter, r *http.Request) {
	reqBody, _ := ioutil.ReadAll(r.Body)
	var loadbalancer LoadBalancer
	json.Unmarshal(reqBody, &loadbalancer)

	// we will need a unique UUID first, then we will need an EIP

	loadbalancer.UUID = uuid.NewString()
	ip, err := getanIP(projectID, "8e6470b3-b75e-47d1-bb93-45b225750975")
	if err != nil {
		log.Error(err)
	} else {
		loadbalancer.EIP = *ip
	}
	err = AddIP(loadbalancer.EIP)
	if err != nil {
		log.Error(err)
	}
	// Do load Balancer magic
	err = bgpConfig.AddHost(fmt.Sprintf("%s/32", loadbalancer.EIP))
	if err != nil {
		log.Error(err)
	}
	loadbalancer.ipvs, err = NewIPVSLB(loadbalancer.EIP, loadbalancer.Port)
	if err != nil {
		log.Error(err)
	}
	LoadBalancers = append(LoadBalancers, loadbalancer)
	log.Infof("Created new Loadbalancer IP [%s] UUID [%s]", loadbalancer.EIP, loadbalancer.UUID)
}

func returnAllLoadBalancers(w http.ResponseWriter, r *http.Request) {
	json.NewEncoder(w).Encode(LoadBalancers)
}

func returnSingleLoadBalancer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["uuid"]

	for _, loadbalancer := range LoadBalancers {
		if loadbalancer.UUID == key {
			json.NewEncoder(w).Encode(loadbalancer)
		}
	}
}

func createNewBackend(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["uuid"]

	for i, loadbalancer := range LoadBalancers {
		if loadbalancer.UUID == key {
			reqBody, _ := ioutil.ReadAll(r.Body)
			fmt.Println(string(reqBody))
			var backend Backends
			backend.UUID = uuid.NewString()
			err := json.Unmarshal(reqBody, &backend)
			if err != nil {
				log.Error(err)
			}
			LoadBalancers[i].Backend = append(loadbalancer.Backend, backend)
			loadbalancer.ipvs.AddBackend(backend.IP, backend.Port)
			log.Infof("Created new backend for IP [%s] w/IP [%s] UUID [%s]", loadbalancer.EIP, backend.IP, backend.UUID)
		}
	}
}

func deleteBackend(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["uuid"]
	backendKey := vars["backenduui"]

	// Loop over all of our Articles
	// if the article.Id equals the key we pass in
	// return the article encoded as JSON
	for _, loadbalancer := range LoadBalancers {
		if loadbalancer.UUID == key {
			for i, backend := range loadbalancer.Backend {
				if backend.UUID == backendKey {
					loadbalancer.ipvs.RemoveBackend(backend.IP)
					loadbalancer.Backend = append(loadbalancer.Backend[:i], loadbalancer.Backend[i+1:]...)

				}

			}

		}
	}
}

func deleteLoadBalancer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["uuid"]

	for index, loadbalancer := range LoadBalancers {
		if loadbalancer.UUID == key {
			// Delete teh load balancer from teh API
			err := delanIP(projectID, loadbalancer.EIP)
			if err != nil {
				log.Error(err)
			}
			err = loadbalancer.ipvs.RemoveIPVSLB()
			if err != nil {
				log.Error(err)
			}
			err = DeleteIP(loadbalancer.EIP)
			if err != nil {
				log.Error(err)
			}
			err = bgpConfig.DelHost(fmt.Sprintf("%s/32", loadbalancer.EIP))
			if err != nil {
				log.Error(err)
			}
			LoadBalancers = append(LoadBalancers[:index], LoadBalancers[index+1:]...)
		}
	}
}

func homePage(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintf(w, "Welcome to the HomePage!")
	fmt.Println("Endpoint Hit: homePage")
}

func getanIP(project, facility string) (*string, error) {
	req := packngo.IPReservationRequest{
		Type:        "public_ipv4",
		Quantity:    1,
		Description: "vippy loadbalancer EIP",
		Facility:    &facility,

		FailOnApprovalRequired: true,
	}

	ipReservation, _, err := emClient.ProjectIPs.Request(project, &req)
	if err != nil {
		return nil, fmt.Errorf("failed to request an IP for the load balancer: %v", err)
	}
	return &ipReservation.Address, nil
}

func delanIP(project, address string) error {
	ips, _, err := emClient.ProjectIPs.List(project, &packngo.ListOptions{})
	if err != nil {
		return err
	}
	for x := range ips {
		if ips[x].Address == address {
			_, err = emClient.ProjectIPs.Remove(ips[x].ID)
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func findSelf(projectID string) *packngo.Device {
	// Go through devices
	dev, _, _ := emClient.Devices.List(projectID, &packngo.ListOptions{})
	for _, d := range dev {
		me, _ := os.Hostname()
		if me == d.Hostname {
			return &d
		}
	}
	return nil
}

func startBGP() (*BGPConfig, error) {
	var b Config
	thisDevice := findSelf(projectID)
	if thisDevice == nil {
		log.Fatalf("Unable to find device/server in Equinix Metal")
	}

	log.Infof("Querying BGP settings for [%s]\n", thisDevice.Hostname)
	neighbours, _, err := emClient.Devices.ListBGPNeighbors(thisDevice.ID, &packngo.ListOptions{})
	if err != nil {
		log.Fatal(err)
	}
	if len(neighbours) == 0 {
		log.Fatalf("There are no neighbours being advertised, ensure BGP is enabled for this device")
	}
	if len(neighbours) > 1 {
		log.Fatalf("There are [%s] neighbours, only designed to manage one", len(neighbours))
	}
	b.RouterID = neighbours[0].CustomerIP
	b.AS = uint32(neighbours[0].CustomerAs)

	// Add the peer(s)
	for x := range neighbours[0].PeerIps {
		peer := Peer{
			Address: neighbours[0].PeerIps[x],
			AS:      uint32(neighbours[0].PeerAs),
		}
		b.Peers = append(b.Peers, peer)
	}

	s, err := NewBgp(&b)

	if err != nil {
		log.Fatal(err)
	}
	return s, nil
}

func NewBgp(c *Config) (b *BGPConfig, err error) {
	if c.AS == 0 {
		return nil, fmt.Errorf("You need to provide AS")
	}

	if c.SourceIP != "" && c.SourceIF != "" {
		return nil, fmt.Errorf("SourceIP and SourceIF are mutually exclusive")
	}

	if len(c.Peers) == 0 {
		return nil, fmt.Errorf("You need to provide at least one peer")
	}

	b = &BGPConfig{
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
func (b *BGPConfig) AddPeer(peer Peer) (err error) {
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

func (b *BGPConfig) getPath(ip net.IP) *api.Path {
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

func (b *BGPConfig) AddHost(addr string) (err error) {
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

func (b *BGPConfig) DelHost(addr string) (err error) {
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

func (b *BGPConfig) Close() error {
	ctx, cf := context.WithTimeout(context.Background(), 5*time.Second)
	defer cf()
	return b.s.StopBgp(ctx, &api.StopBgpRequest{})
}

// AddIP - Add an IP address to the interface
func AddIP(ip string) error {
	link, err := netlink.LinkByName("lo")
	if err != nil {
		return err
	}

	address, err := netlink.ParseAddr(ip + "/32")
	address.Scope = unix.RT_SCOPE_HOST
	if err := netlink.AddrReplace(link, address); err != nil {
		return errors.Wrap(err, "could not add ip")
	}
	return nil
}

// DeleteIP - Remove an IP address from the interface
func DeleteIP(ip string) error {
	link, err := netlink.LinkByName("lo")
	if err != nil {
		return err
	}

	address, err := netlink.ParseAddr(ip + "/32")
	if err = netlink.AddrDel(link, address); err != nil {
		return errors.Wrap(err, "could not delete ip")
	}

	return nil
}

const (
	ROUNDROBIN = "rr"
)

type IPVSLoadBalancer struct {
	client              ipvs.Client
	loadBalancerService ipvs.Service
	Port                int
}

func NewIPVSLB(address string, port int) (*IPVSLoadBalancer, error) {

	// Create IPVS client
	c, err := ipvs.New()
	if err != nil {
		return nil, fmt.Errorf("error creating IPVS client: %v", err)
	}

	// Generate out API Server LoadBalancer instance
	svc := ipvs.Service{
		Family:    ipvs.INET,
		Protocol:  ipvs.TCP,
		Port:      uint16(port),
		Address:   ipvs.NewIP(net.ParseIP(address)),
		Scheduler: ROUNDROBIN,
	}
	err = c.CreateService(svc)
	// If we've an error it could be that the IPVS lb instance has been left from a previous leadership
	if err != nil && strings.Contains(err.Error(), "file exists") {
		log.Warnf("load balancer for API server already exists, attempting to remove and re-create")
		err = c.RemoveService(svc)
		if err != nil {
			return nil, fmt.Errorf("error re-creating IPVS service: %v", err)
		}
		err = c.CreateService(svc)
		if err != nil {
			return nil, fmt.Errorf("error re-creating IPVS service: %v", err)
		}
	} else if err != nil {
		return nil, fmt.Errorf("error creating IPVS service: %v", err)
	}

	lb := &IPVSLoadBalancer{
		Port:                port,
		client:              c,
		loadBalancerService: svc,
	}
	// Return our created load-balancer
	return lb, nil
}

func (lb *IPVSLoadBalancer) RemoveIPVSLB() error {
	err := lb.client.RemoveService(lb.loadBalancerService)
	if err != nil {
		return fmt.Errorf("error removing existing IPVS service: %v", err)
	}
	return nil

}

func (lb *IPVSLoadBalancer) AddBackend(address string, port int) error {
	dst := ipvs.Destination{
		Address:   ipvs.NewIP(net.ParseIP(address)),
		Port:      uint16(port),
		Family:    ipvs.INET,
		Weight:    1,
		FwdMethod: ipvs.Masquarade,
	}

	err := lb.client.CreateDestination(lb.loadBalancerService, dst)
	// Swallow error of existing back end, the node watcher may attempt to apply
	// the same back end multiple times
	if err != nil && !strings.Contains(err.Error(), "file exists") {
		return fmt.Errorf("error creating backend: %v", err)
	}
	return nil
}

func (lb *IPVSLoadBalancer) RemoveBackend(address string) error {
	dst := ipvs.Destination{
		Address: ipvs.NewIP(net.ParseIP(address)),
		Port:    6443,
		Family:  ipvs.INET,
		Weight:  1,
	}
	err := lb.client.RemoveDestination(lb.loadBalancerService, dst)
	if err != nil {
		return fmt.Errorf("error removing backend: %v", err)
	}
	return nil
}

func main() {
	projectID = os.Getenv("PROJECT_ID")
	facility = os.Getenv("FACILITY_ID")

	log.Infoln("Starting Equinix Metal - Vippy LoadBalancer")
	var err error
	log.Infoln("Creating Equinix Metal Client")
	emClient, err = packngo.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	log.Infoln("Creating BGP")
	bgpConfig, err = startBGP()
	if err != nil {
		log.Fatal(err)
	}
	log.Infoln("API Server is now listening on :10001")

	handleRequests()
}
