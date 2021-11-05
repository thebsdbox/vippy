package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/http"
	"os"
	"strconv"

	"github.com/maxence-charriere/go-app/v9/pkg/app"
	"github.com/thebsdbox/vippy/pkg/bgp"
	"github.com/thebsdbox/vippy/pkg/equinixmetal"
	"github.com/thebsdbox/vippy/pkg/kernel"
	"github.com/thebsdbox/vippy/pkg/loadbalancer"
	"github.com/thebsdbox/vippy/pkg/network"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"github.com/packethost/packngo"
	log "github.com/sirupsen/logrus"
)

// curl -X POST http://code:10001/loadbalancer -H 'Content-Type: application/json' -d '{"name":"test loadbalancer", "port": "6443"}'
// curl -X POST http://code:10001/loadbalancer/uuid/backend -H 'Content-Type: application/json' -d '{"ip":"1.1.1.1", "port": "6443"}'

// curl -X POST http://code:10001/loadbalancer/02b5d4d9-f40b-47a1-aa74-91631732bab1/backend -H 'Content-Type: application/json' -d '{"ip":"1.1.1.1", "port": "6443"}'

var projectID, facility string
var manager *bgp.BGPManager
var emClient *packngo.Client

// LoadBalancers is a global variable that holds ALL local loadbalancers
var LoadBalancers []loadBalancer

// LoadBalancer is a loadBalancer instance
type loadBalancer struct {
	UUID string `json:"uuid"`
	Name string `json:"name"`

	EIP  string `json:"eip"`
	Port int    `json:"port"`

	ipvs    *loadbalancer.IPVSLoadBalancer
	Backend []backends `json:"backends"`
}

type backends struct {
	UUID string `json:"uuid"`
	IP   string `json:"ip"`
	Port int    `json:"port"`
}

// hello is a component that displays a simple "Hello World!". A component is a
// customizable, independent, and reusable UI element. It is created by
// embedding app.Compo into a struct.
type hello struct {
	app.Compo
}

// The Render method is where the component appearance is defined. Here, a
// "Hello World!" is displayed as a heading.
func (h *hello) Render() app.UI {
	b, _ := json.MarshalIndent(LoadBalancers, "", "\t")
	return app.Body().Text(b)
	//return app.H1().Text(b)
}

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

	//myRouter.HandleFunc("/", homePage)

	app.Route("/", &hello{})

	// Once the routes set up, the next thing to do is to either launch the app
	// or the server that serves the app.
	//
	// When executed on the client-side, the RunWhenOnBrowser() function
	// launches the app,  starting a loop that listens for app events and
	// executes client instructions. Since it is a blocking call, the code below
	// it will never be executed.
	//
	// When executed on the server-side, RunWhenOnBrowser() does nothing, which
	// lets room for server implementation without the need for precompiling
	// instructions.
	app.RunWhenOnBrowser()

	myRouter.Handle("/", &app.Handler{
		Name:        "Hello",
		Description: "An Hello World! example",
		Image:       "https://dka575ofm4ao0.cloudfront.net/pages-transactional_logos/retina/8704/Equinix_Metal_Standard_RGB.png",
	})

	// finally, instead of passing in nil, we want
	// to pass in our newly created router as the second
	// argument
	log.Fatal(http.ListenAndServe(":10001", myRouter))
}

func createNewLoadBalancer(w http.ResponseWriter, r *http.Request) {
	var err error
	reqBody, _ := ioutil.ReadAll(r.Body)
	var newLB loadBalancer
	json.Unmarshal(reqBody, &newLB)

	// we will need a unique UUID first, then we will need an EIP

	newLB.UUID = uuid.NewString()
	demoMode := os.Getenv("DEMO")
	demo, _ := strconv.ParseBool(demoMode)
	var ip *string
	blank := "0.0.0.0"
	ip = &blank
	if !demo {
		ip, err = equinixmetal.GetEIP(emClient, projectID, facility)
	}
	if err != nil {
		log.Error(err)
	} else {
		newLB.EIP = *ip
	}
	err = network.AddIP(newLB.EIP)
	if err != nil {
		log.Error(err)
	}
	// Do load Balancer magic
	err = manager.AddHost(fmt.Sprintf("%s/32", newLB.EIP))
	if err != nil {
		log.Error(err)
	}
	newLB.ipvs, err = loadbalancer.NewIPVSLB(newLB.EIP, newLB.Port)
	if err != nil {
		log.Error(err)
	}
	LoadBalancers = append(LoadBalancers, newLB)
	log.Infof("Created new Loadbalancer IP [%s] UUID [%s]", newLB.EIP, newLB.UUID)
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
			var backend backends
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
			err := equinixmetal.DelEIP(emClient, projectID, loadbalancer.EIP)
			if err != nil {
				log.Error(err)
			}
			err = loadbalancer.ipvs.RemoveIPVSLB()
			if err != nil {
				log.Error(err)
			}
			err = network.DeleteIP(loadbalancer.EIP)
			if err != nil {
				log.Error(err)
			}
			err = manager.DelHost(fmt.Sprintf("%s/32", loadbalancer.EIP))
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

func configureNetwork() error {

	err := kernel.EnableMasq()
	if err != nil {
		return err
	}
	err = kernel.ConfigureIPForwarding()
	if err != nil {
		return err
	}
	err = kernel.ConfigureConntrack()
	if err != nil {
		return err
	}
	return nil
}

func main() {
	projectID = os.Getenv("PROJECT_ID")
	facility = os.Getenv("FACILITY_ID")

	log.Infoln("Starting Equinix Metal - Vippy LoadBalancer")
	var err error
	log.Infoln("Connecting to Equinix Metal API")
	emClient, err = packngo.NewClient()
	if err != nil {
		log.Fatal(err)
	}

	log.Info("Configuring kernel for forwarding and masquerading of packets")
	err = configureNetwork()
	if err != nil {
		log.Fatal(err)
	}

	bgpConfig, err := equinixmetal.FindBGPConfig(emClient, projectID)
	if err != nil {
		log.Fatal(err)
	}
	log.Infoln("Creating BGP")

	manager, err = bgp.NewBgp(bgpConfig)
	if err != nil {
		log.Fatal(err)
	}

	log.Infoln("API Server is now listening on :10001")
	handleRequests()
}
