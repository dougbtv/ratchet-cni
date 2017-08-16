// Copyright 2015 CNI authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// This is a fork of Multus-CNI, which is a fork of Flannel
// This is a fork of a spoon.
// This is an implementation of Koko as a CNI plugin.
// Original Author @dougbtv

package main

import (
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	// "reflect"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/containernetworking/cni/pkg/invoke"
	"github.com/containernetworking/cni/pkg/skel"
	"github.com/containernetworking/cni/pkg/types"
	"github.com/containernetworking/cni/pkg/version"
	"golang.org/x/net/context"

	"github.com/davecgh/go-spew/spew"
	dockerclient "github.com/docker/docker/client"
	// "github.com/redhat-nfvpe/koko"
	"github.com/coreos/etcd/client"
)

const defaultCNIDir = "/var/lib/cni/multus"

// DEBUG Set to enable some debugging output
const DEBUG = true

// PerformDelete tell us to actually do anything on CNI delete instruction (not implemented, yet)
const PerformDelete = false

var kapi client.KeysAPI

var logger = log.New(os.Stderr, "", 0)

var masterpluginEnabled bool

// NetConf is our network configuration as passed in as json
type NetConf struct {
	types.NetConf
	CNIDir      string                 `json:"cniDir"`
	Delegate    map[string]interface{} `json:"delegate"`
	EtcdHost    string                 `json:"etcd_host"`
	EtcdPort    string                 `json:"etcd_port"`
	UseLabels   bool                   `json:"use_labels"`
	ChildPath   string                 `json:"child_path"`
	BootNetwork map[string]interface{} `json:"boot_network"`
	ParentIface string                 `json:"parent_interface"`
	ParentAddr  string                 `json:"parent_address"`
}

// LinkInfo defines the paid of links we're going to create
type LinkInfo struct {
	PodName         string
	TargetPod       string
	TargetContainer string
	PublicIP        string
	LocalIP         string
	LocalIFName     string
	PairName        string
	PairIP          string
	PairIFName      string
	Primary         string
}

//taken from cni/plugins/meta/flannel/flannel.go
func isString(i interface{}) bool {
	_, ok := i.(string)
	return ok
}

func isBool(i interface{}) bool {
	_, ok := i.(bool)
	return ok
}

func loadNetConf(bytes []byte) (*NetConf, error) {
	netconf := &NetConf{}
	if err := json.Unmarshal(bytes, netconf); err != nil {
		return nil, fmt.Errorf("failed to load netconf: %v", err)
	}

	// dump_netconf := spew.Sdump(netconf)
	// logger.Printf("DOUG !trace netconf ----------%v\n",dump_netconf)

	if netconf.Delegate == nil {
		return nil, fmt.Errorf(`"delegate" is a required field in config, it should be the config of the main plugin to use`)
	}

	if netconf.CNIDir == "" {
		netconf.CNIDir = defaultCNIDir
	}

	return netconf, nil

}

func saveScratchNetConf(containerID, dataDir string, netconf []byte) error {
	if err := os.MkdirAll(dataDir, 0700); err != nil {
		return fmt.Errorf("failed to create the multus data directory(%q): %v", dataDir, err)
	}

	path := filepath.Join(dataDir, containerID)

	err := ioutil.WriteFile(path, netconf, 0600)
	if err != nil {
		return fmt.Errorf("failed to write container data in the path(%q): %v", path, err)
	}

	return err
}

func consumeScratchNetConf(containerID, dataDir string) ([]byte, error) {
	path := filepath.Join(dataDir, containerID)
	defer os.Remove(path)

	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read container data in the path(%q): %v", path, err)
	}

	return data, err
}

func getifname() (f func() string) {
	var interfaceIndex int
	f = func() string {
		ifname := fmt.Sprintf("net%d", interfaceIndex)
		interfaceIndex++
		return ifname
	}

	return
}

func saveDelegates(containerID, dataDir string, delegates []map[string]interface{}) error {
	delegatesBytes, err := json.Marshal(delegates)
	if err != nil {
		return fmt.Errorf("error serializing delegate netconf: %v", err)
	}

	if err = saveScratchNetConf(containerID, dataDir, delegatesBytes); err != nil {
		return fmt.Errorf("error in saving the  delegates : %v", err)
	}

	return err
}

func checkDelegate(netconf map[string]interface{}) error {
	if netconf["type"] == nil {
		return fmt.Errorf("delegate must have the field 'type'")
	}

	if !isString(netconf["type"]) {
		return fmt.Errorf("delegate field 'type' must be a string")
	}

	if netconf["masterplugin"] != nil {
		if !isBool(netconf["masterplugin"]) {
			return fmt.Errorf("delegate field 'masterplugin' must be a bool")
		}
	}

	if netconf["masterplugin"] != nil {
		if netconf["masterplugin"].(bool) != false && masterpluginEnabled != true {
			masterpluginEnabled = true
		} else if netconf["masterplugin"].(bool) != false && masterpluginEnabled == true {
			return fmt.Errorf("only one delegate can have 'masterplugin'")
		}
	}
	return nil
}

func isMasterplugin(netconf map[string]interface{}) bool {
	if netconf["masterplugin"] == nil {
		return false
	}

	if netconf["masterplugin"].(bool) == true {
		return true
	}

	return false
}

// !bang
func delegateAdd(podif func() string, argif string, netconf map[string]interface{}, onlyMaster bool) (bool, error) {
	netconfBytes, err := json.Marshal(netconf)
	if err != nil {
		return true, fmt.Errorf("Ratchet: error serializing multus delegate netconf: %v", err)
	}

	if os.Setenv("CNI_IFNAME", argif) != nil {
		return true, fmt.Errorf("Ratchet: error in setting CNI_IFNAME")
	}

	result, err := invoke.DelegateAdd(netconf["type"].(string), netconfBytes)
	if err != nil {
		return true, fmt.Errorf("Ratchet: error in invoke Delegate add - %q: %v", netconf["type"].(string), err)
	}

	return false, result.Print()

}

func delegateDel(podif func() string, argif string, netconf map[string]interface{}) error {
	netconfBytes, err := json.Marshal(netconf)
	if err != nil {
		return fmt.Errorf("Ratchet: error serializing multus delegate netconf: %v", err)
	}

	if !isMasterplugin(netconf) {
		if os.Setenv("CNI_IFNAME", podif()) != nil {
			return fmt.Errorf("Ratchet: error in setting CNI_IFNAME")
		}
	} else {
		if os.Setenv("CNI_IFNAME", argif) != nil {
			return fmt.Errorf("Ratchet: error in setting CNI_IFNAME")
		}
	}

	err = invoke.DelegateDel(netconf["type"].(string), netconfBytes)
	if err != nil {
		return fmt.Errorf("Ratchet: error in invoke Delegate del - %q: %v", netconf["type"].(string), err)
	}

	return err
}

func clearPlugins(mIdx int, pIdx int, argIfname string, delegates []map[string]interface{}) error {

	if os.Setenv("CNI_COMMAND", "DEL") != nil {
		return fmt.Errorf("Ratchet: error in setting CNI_COMMAND to DEL")
	}

	podifName := getifname()
	r := delegateDel(podifName, argIfname, delegates[mIdx])
	if r != nil {
		return r
	}

	for idx := 0; idx < pIdx && idx != mIdx; idx++ {
		r := delegateDel(podifName, argIfname, delegates[idx])
		if r != nil {
			return r
		}
	}

	return nil
}

// func printResults(delresult *types.Result) error {
// 	return delresult.Print()
// }

func ratchet(netconf *NetConf, argif string, containerid string) error {

	var result error
	// Alright first few things:
	// 1. Here is where I'd add that we check the k8s api
	//    in order to see if there's a label that makes this applicable to ratcheting.
	//    other wise it'd just get the delegate.
	//    let's just delegate everything first, though.

	// ------------------------ Delegate "boot network"
	// -- this interface defined the "delegate" section in the config
	// -- defines which interface is used for "regular network access"

	// --------------------- Setup delegate.

	// This is a weak check, fwiw.
	if err := checkDelegate(netconf.Delegate); err != nil {
		return fmt.Errorf("Ratchet delegate: Err in delegate conf: %v", err)
	}

	if err := checkDelegate(netconf.BootNetwork); err != nil {
		return fmt.Errorf("Ratchet BootNetwork: Err in delegate conf: %v", err)
	}

	podifName := getifname()

	// ------------------------ Check label
	ctx := context.Background()
	cli, errDocker := dockerclient.NewEnvClient()
	if errDocker != nil {
		panic(errDocker)
	}

	// cli.UpdateClientVersion("1.24")
	cli.NegotiateAPIVersion(ctx)
	
	json, dockerclienterr := cli.ContainerInspect(ctx, containerid)
	if dockerclienterr != nil {
		return fmt.Errorf("Dockerclient err: %v", dockerclienterr)
	}

	logger.Printf("DOUG !trace json >>>>>>>>>>>>>>>>>>>>>----------%v\n", json.Config.Labels)

	// ------------------------ Determine container eligibility.

	if _, useRatchet := json.Config.Labels["ratchet"]; useRatchet {

		logger.Println("USE RATCHET ----------------------->>>>>>>>>>>>>>>")
		// We want to use the ratchet "boot_network"
		// !bang
		err, r := delegateAdd(podifName, argif, netconf.BootNetwork, false)
		if err != true {
			result = r
		} else if (err != false) && r != nil {
			logger.Printf("ratchet delegateAdd boot_network error ----------%v / %v", err, r)
			return r
		}

	} else {

		// We used to switch here depending on the use_labels flag, which is seemingly obsolete.
		// But, now, we just use the delegate.
		// var mIndex int
		logger.Println("DO NOT USE RATCHET (passthrough)----------------------->>>>>>>>>>>>>>>")
		err, r := delegateAdd(podifName, argif, netconf.Delegate, false)
		if err != true {
			result = r
		} else if (err != false) && r != nil {
			logger.Printf("ratchet delegateAdd passthrough error ----------%v", err)
			return r
		}

		return r

	}

	// If you get to this point -- you're eligible for treatment under ratchet.

	// Populate all the possible link info.

	linki := LinkInfo{}
	linki.PodName = json.Config.Labels["ratchet.pod_name"]
	linki.TargetPod = json.Config.Labels["ratchet.target_pod"]
	linki.TargetContainer = json.Config.Labels["ratchet.target_container"]
	linki.PublicIP = json.Config.Labels["ratchet.public_ip"]
	linki.LocalIP = json.Config.Labels["ratchet.local_ip"]
	linki.LocalIFName = json.Config.Labels["ratchet.local_ifname"]
	linki.PairName = json.Config.Labels["ratchet.pair_name"]
	linki.PairIP = json.Config.Labels["ratchet.pair_ip"]
	linki.PairIFName = json.Config.Labels["ratchet.pair_ifname"]
	linki.Primary = json.Config.Labels["ratchet.primary"]

	dumpLinki := spew.Sdump(linki)
	logger.Printf("...............DOUG !trace linki ----------%v\n", dumpLinki)

	// Spawn external process.
	// ...pass tons of link info along with some basics.

	logger.Printf("executing path: %v / argif: %v / containerID: %v / etcd_host: %v", netconf.ChildPath, argif, containerid, netconf.EtcdHost)
	// exec_string := netconf.ChildPath + " " + argif + " " + containerid + " " + netconf.EtcdHost
	// logger.Printf("executing path composite: %v",exec_string);
	cmd := exec.Command(
		netconf.ChildPath, // 0
		argif,             // 1
		containerid,
		netconf.EtcdHost,
		netconf.EtcdPort,
		linki.PodName,
		linki.TargetPod,
		linki.TargetContainer,
		linki.PublicIP,
		linki.LocalIP,
		linki.LocalIFName,
		linki.PairName,
		linki.PairIP,
		linki.PairIFName,
		linki.Primary,
		netconf.ParentIface,
		netconf.ParentAddr,
	)
	cmd.Start()

	logger.Println("COMPLETE RATCHET CHILD???? ----------------------->>>>>>>>>>>>>>>")

	return result

}

func cmdAdd(args *skel.CmdArgs) error {

	var result error
	n, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	// Pass a pointer to the NetConf type.
	// logger.Println(reflect.TypeOf(n))
	rerr := ratchet(n, args.IfName, args.ContainerID)
	if rerr != nil {
		logger.Printf("Ratchet error from cmdAdd handler: %v", rerr)
		return rerr
	}

	return result

}

func cmdDel(args *skel.CmdArgs) error {
	var result error
	in, err := loadNetConf(args.StdinData)
	if err != nil {
		return err
	}

	if PerformDelete {
		result = ratchet(in, args.IfName, args.ContainerID)
	}

	// TODO: This doesn't perform any cleanup.
	// r := delegateDel(podifName, args.IfName, delegate)

	return result
}

func versionInfo(args *skel.CmdArgs) error {

	var result error
	fmt.Fprintln(os.Stderr, "Version v0.0.0")
	return result

}

func main() {

	if DEBUG {
		logger.Println("[LOGGING ENABLED]")
	}

	skel.PluginMain(cmdAdd, cmdDel, version.All)
}
