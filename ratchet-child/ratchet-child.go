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
	// "encoding/json"
	"fmt"
	"log"
	"time"
	// "io/ioutil"
	// "reflect"
	"os"
	"os/exec"
	// "path/filepath"

	// "github.com/containernetworking/cni/pkg/invoke"
	// "github.com/containernetworking/cni/pkg/skel"
	// "github.com/containernetworking/cni/pkg/types"
	"golang.org/x/net/context"

	// dockerclient "github.com/docker/docker/client"
	// "github.com/davecgh/go-spew/spew"
	"github.com/coreos/etcd/client"
	koko "github.com/redhat-nfvpe/koko/api"
)

const aliveWaitSeconds = 1
const aliveWaitRetries = 60
const delayKokoSeconds = 1
const defaultCNIDir = "/var/lib/cni/multus"
const debug = true

var kapi client.KeysAPI

var masterpluginEnabled bool

// LinkInfo is a detail of the link we're going to create.
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

func isContainerAlive(containername string) bool {
	isalive := false

	targetKey := "/ratchet/byname/" + containername
	_, err := kapi.Get(context.Background(), targetKey, nil)
	if err != nil {

		// ErrorCodeKeyNotFound = Key not found, that's exactly the one we know is good.
		// So let's log when it's not that.
		// Passing along on this.
		/*
			 if (err != client.ErrorCodeKeyNotFound) {
				 logger.Println(fmt.Errorf("isContainerAlive - possible missing value %s: %v", targetKey, err))
			 }
		*/

	} else {
		// no error? must be there.
		isalive = true
	}

	return isalive

}

func amIAlive(containerid string) bool {
	isalive := false

	targetKey := "/ratchet/" + containerid + "/pod_name"
	_, err := kapi.Get(context.Background(), targetKey, nil)
	if err != nil {

		// ErrorCodeKeyNotFound = Key not found, that's exactly the one we know is good.
		// So let's log when it's not that.
		// Passing along on this.
		/*
			 if (err != client.ErrorCodeKeyNotFound) {
				 logger.Println(fmt.Errorf("isContainerAlive - possible missing value %s: %v", targetKey, err))
			 }
		*/

	} else {
		// no error? must be there.
		isalive = true
	}

	return isalive

}

// get the full meta data for a container given the containerid
// if setalive is true, it also sets an "isalive" flag for the container.
// is the isalive necessary?
func getEtcdMetaData(containerid string, setalive bool) map[string]string {

	all := make(map[string]string)

	// All the properties we can have.
	all["pod_name"] = ""
	all["pair_name"] = ""
	all["public_ip"] = ""
	all["local_ip"] = ""
	all["local_ifname"] = ""
	all["pair_ip"] = ""
	all["pair_ifname"] = ""
	all["primary"] = ""
	all["isalive"] = ""

	for k := range all {
		// Print all possibilities...
		// logger(fmt.Sprintf("key[%s] value[%s]\n", k, v))

		// get a key's value
		// logger.Print("Getting '/ratchet/" + containerid + "/" + k + "' key value")
		getcfg := &client.GetOptions{Recursive: true}
		targetKey := "/ratchet/" + containerid + "/" + k
		messageResp, err := kapi.Get(context.Background(), targetKey, getcfg)
		if err != nil {

			// For now, this seems to be just the missing values.
			// ...which are generally fine.
			// logger.Println(fmt.Errorf("possible missing value %s: %v", targetKey, err))

		} else {
			// print common key info
			// logger(fmt.Sprintf("Get is done. Metadata is %q\n", messageResp))
			// print value

			// dump_resp := spew.Sdump(messageResp)
			// os.Stderr.WriteString("DOUG !trace messageResp ----------\n" + dump_resp)

			// dump_adata := spew.Sdump(adata)
			// os.Stderr.WriteString("DOUG !trace adata ----------\n" + dump_adata)

			all[k] = messageResp.Node.Value

			// logger(fmt.Sprintf("%q key has %q value\n", messageResp.Node.Key, messageResp.Node.Value))
		}

	}

	if setalive {

		// set "/foo" key with "bar" value
		// log.Print("Setting '/foo' key with 'bar' value")
		_, err := kapi.Set(context.Background(), "/ratchet/"+containerid+"/isalive", "true", nil)
		if err != nil {
			log.Fatal(err)
		} else {
			// print common key info
			// log.Printf("Set is done. Metadata is %q\n", resp)
		}

	}

	return all

}

func associateIDEtcd(containerid string, podname string) error {

	// set "/foo" key with "bar" value
	// log.Print("Setting '/foo' key with 'bar' value")
	_, err := kapi.Set(context.Background(), "/ratchet/association/"+podname, containerid, nil)
	if err != nil {
		log.Fatal(err)
		return err
	}

	return nil

}

func isPairContainerAlive(podname string) string {

	targetKey := "/ratchet/association/" + podname
	respContainerID, err := kapi.Get(context.Background(), targetKey, nil)
	if err != nil {

		// ErrorCodeKeyNotFound = Key not found, that's exactly the one we know is good.
		// So let's log when it's not that.
		// Passing along on this.
		/*
			 if (err != client.ErrorCodeKeyNotFound) {
				 logger.Println(fmt.Errorf("isPairContainerAlive - possible missing value %s: %v", targetKey, err))
			 }
		*/

	} else {
		// no error? must be there.
		return respContainerID.Node.Value
	}

	return ""

}

func ratchet(argif string, containerid string, linki LinkInfo) error {

	os.Stderr.WriteString("!trace alive The containerid: " + containerid + "\n")

	// We no longer care if we're alive anymore.
	// If this is up, we can assume the infra container is good to go.
	// So all we need to do is associate our containerid with our name.
	associateIDEtcd(containerid, linki.PodName)

	// If it's determined that we're alive, now we can see if we're primary.
	// If we're not primary, we can just exit right now.
	// Cause the primary side will add to this pair.

	if linki.Primary != "true" {
		// Ok, we're not primary. So... time to exit.
		if debug {
			logger(fmt.Sprintf("Normal termination, this container is not primary (name: %v, containerid: %v, primary: %v)", linki.PodName, containerid, linki.Primary))
		}

		return nil
	}

	// Check to see there's a valid pair name.
	if len(linki.PairName) <= 1 {
		// That's not good.
		return fmt.Errorf("Pair name appears to be invalid: %v", linki.PairName)
	}

	// Now we want to check and see if the pair container is alive.
	// So it's time to go into a loop and do that.
	// if the pair container is alive -- bada bing, we can execute koko.

	var pairContainerID string
	tries := 0

	for {

		pairContainerID = isPairContainerAlive(linki.PairName)

		if len(pairContainerID) >= 1 {
			// We found it.
			break
		}

		tries++

		if debug {
			logger(fmt.Sprintf("Is pair alive? pair_name: %v (%v retries)\n", linki.PairName, tries))
		}

		// We either timeout, or, we're alive.
		if tries >= aliveWaitRetries {
			return fmt.Errorf("Timeout: could not find that pair container is alive via metadata in %v tries", tries)
		}

		// Wait for however long.
		time.Sleep(aliveWaitSeconds * time.Second)

	}

	// Now, we can probably rock out all the
	logger(fmt.Sprintf("And my pair's container id is: %v", pairContainerID))

	// dump_my_meta := spew.Sdump(my_meta)
	// os.Stderr.WriteString("The containerid: " + containerid + "\n")
	// os.Stderr.WriteString("DOUG !trace my_meta ----------\n" + dump_my_meta)
	// os.Stderr.WriteString("DOUG !trace pair_alive ----------" + fmt.Sprintf("%t",pair_alive) + "\n")

	// !trace !bang
	// This is how you call up koko.
	// koko.VethCreator("foo","192.168.2.100/24","in1","bar","192.168.2.101/24","in2")

	// What about a healthy delay?
	logger(fmt.Sprintf("Pre koko-delay, %v SECONDS", delayKokoSeconds))
	time.Sleep(delayKokoSeconds * time.Second)

	veth1 := koko.VEth{}
	veth2 := koko.VEth{}

	veth1.NsName = containerid
	veth1.IPAddr = linki.LocalIP + "/24"
	veth1.LinkName = linki.LocalIFName

	veth1.NsName = pairContainerID
	veth1.IPAddr = linki.PairIP + "/24"
	veth1.LinkName = linki.PairIFName

	koko.makeVeth(veth1, veth2)

	// kokoErr := koko.VethCreator(
	// 	containerid,
	// 	linki.LocalIP+"/24",
	// 	linki.LocalIFName,
	// 	pairContainerID,
	// 	linki.PairIP+"/24",
	// 	linki.PairIFName,
	// )

	if kokoErr != nil {
		logger(fmt.Sprintf("koko error in child: %v", kokoErr))
		return kokoErr
	}

	logger("Koko appears to have completed with success.")

	return nil

}

func logger(input string) {

	// exec_command :=
	// os.Stderr.WriteString("!trace alive The containerid: |" + exec_command + "|||\n")

	// This is a total hack.
	// cmd := exec.Command("/bin/bash", "-c", "echo \"ratchet-child: " + input + "\" | systemd-cat")

	// This is even MORE of a hack.
	cmd := exec.Command("/bin/bash", "-c", "echo \"ratchet-child: "+input+"\" >> /tmp/ratchet-child.log")
	cmd.Start()

}

func initEtcd(etcdHost string, etcdPort string) {

	// Make a connection to etcd. Then we reuse the "kapi"

	cfg := client.Config{
		Endpoints: []string{"http://" + etcdHost + ":" + etcdPort},
		Transport: client.DefaultTransport,
		// set timeout per request to fail fast when the target endpoint is unavailable
		HeaderTimeoutPerRequest: time.Second,
	}
	c, err := client.New(cfg)
	if err != nil {
		log.Fatal(err)
	}
	kapi = client.NewKeysAPI(c)
}

func main() {

	// arg := os.Args[3]

	initEtcd(os.Args[3], os.Args[4])

	if debug {
		logger("[LOGGING ENABLED]")
		logger(fmt.Sprintf("Interface: %v", os.Args[1]))
		logger(fmt.Sprintf("ContainerID: %v", os.Args[2]))
		// Inspect all the arguments.
		for idx, element := range os.Args {
			logger(fmt.Sprintf("arg[%v]: %v", idx, element))
		}
	}

	// Apr 29 00:43:30 cni unknown[14497]: ratchet-child: arg[0]: /home/centos/cni/bin/ratchet-child
	// Apr 29 00:43:30 cni unknown[14499]: ratchet-child: arg[1]: eth0
	// Apr 29 00:43:30 cni unknown[14503]: ratchet-child: arg[2]: a3a3cb196700434c0ff58f0c07c8c51b95d2404826a017799b49c6a61224af1a
	// Apr 29 00:43:30 cni cat[14507]: ratchet-child: arg[3]: localhost
	// Apr 29 00:43:30 cni cat[14510]: ratchet-child: arg[4]: primary-pod
	// Apr 29 00:43:30 cni unknown[14515]: ratchet-child: arg[5]: primary-pod
	// Apr 29 00:43:30 cni unknown[14518]: ratchet-child: arg[6]: primary-pod
	// Apr 29 00:43:30 cni unknown[14522]: ratchet-child: arg[7]: 1.1.1.1
	// Apr 29 00:43:30 cni cat[14524]: ratchet-child: arg[8]: 192.168.2.100
	// Apr 29 00:43:30 cni cat[14527]: ratchet-child: arg[9]: in1
	// Apr 29 00:43:30 cni cat[14532]: ratchet-child: arg[10]: pair-pod
	// Apr 29 00:43:30 cni cat[14539]: ratchet-child: arg[13]: true
	// Apr 29 00:43:30 cni unknown[14537]: ratchet-child: arg[12]: in2
	// Apr 29 00:43:30 cni unknown[14534]: ratchet-child: arg[11]: 192.168.2.101

	linki := LinkInfo{}
	linki.PodName = os.Args[5]
	linki.TargetPod = os.Args[6]
	linki.TargetContainer = os.Args[7]
	linki.PublicIP = os.Args[8]
	linki.LocalIP = os.Args[9]
	linki.LocalIFName = os.Args[10]
	linki.PairName = os.Args[11]
	linki.PairIP = os.Args[12]
	linki.PairIFName = os.Args[13]
	linki.Primary = os.Args[14]

	err := ratchet(os.Args[1], os.Args[2], linki)
	if err != nil {
		logger("completition WITH ERROR")
		logger(fmt.Sprintf("%v", err))
	} else {
		logger("ratchet completition, success. (containerid: " + os.Args[2] + ")")
	}

}
