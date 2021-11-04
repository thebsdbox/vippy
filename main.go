package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	api "github.com/osrg/gobgp/api"
	gobgp "github.com/osrg/gobgp/pkg/server"
	"github.com/packethost/packngo"
	log "github.com/sirupsen/logrus"
)

// curl -X POST http://code:10001/loadbalancer -H 'Content-Type: application/json' -d '{"name":"test loadbalancer"}'
// curl -X POST http://code:10001/loadbalancer/uuid/backend -H 'Content-Type: application/json' -d '{"ip":"1.1.1.1", port: "6443"}'

// curl -X POST http://code:10001/loadbalancer/02b5d4d9-f40b-47a1-aa74-91631732bab1/backend -H 'Content-Type: application/json' -d '{"ip":"1.1.1.1", "port": "6443"}'

var projectID string
var bgpConfig BGPConfig
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
	UUID    string     `json:"uuid"`
	Name    string     `json:"name"`
	EIP     string     `json:"eip"`
	Backend []Backends `json:"backends"`
}

type Backends struct {
	UUID string `json:"uuid"`
	IP   string `json:"ip"`
	Port string `json:"port"`
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
	LoadBalancers = append(LoadBalancers, loadbalancer)

	json.NewEncoder(w).Encode(loadbalancer)
}

func returnAllLoadBalancers(w http.ResponseWriter, r *http.Request) {
	fmt.Println("Endpoint Hit: returnAllArticles")
	json.NewEncoder(w).Encode(LoadBalancers)
}

func returnSingleLoadBalancer(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["uuid"]

	// Loop over all of our Articles
	// if the article.Id equals the key we pass in
	// return the article encoded as JSON
	for _, loadbalancer := range LoadBalancers {
		if loadbalancer.UUID == key {
			json.NewEncoder(w).Encode(loadbalancer)
		}
	}
}

func createNewBackend(w http.ResponseWriter, r *http.Request) {
	vars := mux.Vars(r)
	key := vars["uuid"]

	// Loop over all of our Articles
	// if the article.Id equals the key we pass in
	// return the article encoded as JSON
	for i, loadbalancer := range LoadBalancers {
		if loadbalancer.UUID == key {
			reqBody, _ := ioutil.ReadAll(r.Body)
			fmt.Printf(string(reqBody))
			var backend Backends
			backend.UUID = uuid.NewString()
			err := json.Unmarshal(reqBody, &backend)
			if err != nil {
				log.Error(err)
			}
			LoadBalancers[i].Backend = append(loadbalancer.Backend, backend)
			json.NewEncoder(w).Encode(LoadBalancers[i])
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

func startBGP() (*Server, error) {
	thisDevice := findSelf(projectID)
	if thisDevice == nil {
		log.Fatalf("Unable to find correct device")
	}

	fmt.Printf("Querying BGP settings for [%s]", thisDevice.Hostname)
	neighbours, _, _ := emClient.Devices.ListBGPNeighbors(thisDevice.ID, &packngo.ListOptions{})
	if len(neighbours) > 1 {
		log.Fatalf("There are [%s] neighbours, only designed to manage one", len(neighbours))
	}

	bgpCfg := &Config{
		RouterID: neighbours[0].CustomerIP,
		AS:       uint32(neighbours[0].CustomerAs),
	}
	p := &Peer{
		Address: neighbours[0].PeerIps[0],
		AS:      uint32(neighbours[0].PeerAs),
	}

	bgpCfg.Peers = append(bgpCfg.Peers, *p)
	s, err := NewBgp(bgpCfg)

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

	b = &Server{
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
func (b *Server) AddPeer(peer Peer) (err error) {
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

	return b.s.AddPeer(context.Background(), &api.AddPeerRequest{
		Peer: p,
	})
}
func main() {
	projectID = os.Getenv("PROJ_ID")
	log.Infoln("Starting Equinix Metal - Vippy LoadBalancer")
	var err error
	log.Infoln("Creating Equinix Metal Client")
	emClient, err = packngo.NewClient()
	if err != nil {
		log.Fatal(err)
	}
	log.Infoln("Creating BGP")
	bgpServer, err = BGPClient()
	handleRequests()
}
