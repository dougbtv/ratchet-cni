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
	"net"
	"time"
	// "io/ioutil"
	// "reflect"
	"os"
	"os/exec"
	"strconv"
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
const beginningVxlanID = 11

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
	ParentIface     string
	ParentAddr      string
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

func associateEtcdInfo(containerid string, linki LinkInfo) (int,error) {

	var vxlanid = 0

	// Associate the containerid to the name.
	_, err := kapi.Set(context.Background(), "/ratchet/association/"+linki.PodName+"/id", containerid, nil)
	if err != nil {
		logger(fmt.Sprintf("SETETCD ASSOC ERROR: %v", err))
		return 0,err
	}

	_, err2 := kapi.Set(context.Background(), "/ratchet/association/"+linki.PodName+"/parentiface", linki.ParentIface, nil)
	if err2 != nil {
		logger(fmt.Sprintf("SETETCD parentiface ERROR: %v", err2))
		return 0,err2
	}

	_, err3 := kapi.Set(context.Background(), "/ratchet/association/"+linki.PodName+"/parentaddr", linki.ParentAddr, nil)
	if err3 != nil {
		logger(fmt.Sprintf("SETETCD parentaddr ERROR: %v", err3))
		return 0,err3
	}

	// Things the primary also stores....
	if linki.Primary == "true" {

		// we should handle the vxlan id now.
		vxlanid, _ = getVxLanID()

		// and we have to store it.
		_, err5 := kapi.Set(context.Background(), "/ratchet/association/"+linki.PairName+"/vxlanid", strconv.Itoa(vxlanid), nil)
		if err5 != nil {
			logger(fmt.Sprintf("SETETCD vxlanid assoc ERROR: %v", err5))
			return 0,err5
		}

		// and we have to save the pair IP.
		_, err6 := kapi.Set(context.Background(), "/ratchet/association/"+linki.PairName+"/pairip", linki.PairIP, nil)
		if err6 != nil {
			logger(fmt.Sprintf("SETETCD primaryname ERROR: %v", err6))
			return 0,err6
		}

		// and we have to save the pair interface.
		_, err7 := kapi.Set(context.Background(), "/ratchet/association/"+linki.PairName+"/pairifname", linki.PairIFName, nil)
		if err7 != nil {
			logger(fmt.Sprintf("SETETCD primaryname ERROR: %v", err7))
			return 0,err7
		}

		// we're primary, we need to let the pair know how to find the primary.
		_, err4 := kapi.Set(context.Background(), "/ratchet/association/"+linki.PairName+"/primaryname", linki.PodName, nil)
		if err4 != nil {
			logger(fmt.Sprintf("SETETCD primaryname ERROR: %v", err4))
			return 0,err4
		}

	}

	return vxlanid, nil

}

func getVxLanParentInfo(podname string) (string, string,error) {

	var parentiface = ""
	var parentaddr = ""

	targetKey := "/ratchet/association/" + podname + "/parentiface"
	ifResp, err := kapi.Get(context.Background(), targetKey, nil)
	if err != nil {
		logger(fmt.Sprintf("ERROR GETTING PARENTIF FROM ETCD: %v / %v @ %v", podname, err, targetKey))
		return parentiface, parentaddr, err
	}
	
	// We properly have a parent interface.
	parentiface = ifResp.Node.Value
	
	targetKey2 := "/ratchet/association/" + podname + "/parentaddr"
	addrResp, err2 := kapi.Get(context.Background(), targetKey2, nil)
	if err2 != nil {
		logger(fmt.Sprintf("ERROR GETTING PARENTADDR FROM ETCD: %v / %v", podname, err2, targetKey2))
		return parentiface, parentaddr, err2
	}
	
	parentaddr = addrResp.Node.Value
	
	return parentiface, parentaddr, nil

}

func getVxLanID() (int, error) {

	targetKey := "/ratchet/vxlanid"
	vxlanidResp, err := kapi.Get(context.Background(), targetKey, nil)
	if err != nil {
		// if (err != client.ErrorCodeKeyNotFound) {
		// 	// That's a real error?
		// 	logger("UNEXCEPTED getVxLan-Id ERROR CODE? %v",err)
		// }

		// Let's assume that means we don't have this set.
		// so let's default it.
		_, err2 := kapi.Set(context.Background(), targetKey, strconv.Itoa(beginningVxlanID+1), nil)
		if err2 != nil {
			logger(fmt.Sprintf("SETETCD getVxLan-Id ERROR: %v", err2))
			return 0, err2
		}

		return beginningVxlanID, nil

	}

	// Ok pick up the current one.
	vxlanstring := vxlanidResp.Node.Value

	// Convert it to an int
	vxlanid, _ := strconv.Atoi(vxlanstring)

	// Increment it, and set it.
	_, err2 := kapi.Set(context.Background(), targetKey, strconv.Itoa(vxlanid+1), nil)
	if err2 != nil {
		logger(fmt.Sprintf("SETETCD increment getVxLan-Id ERROR: %v", err2))
		return 0, err2
	}

	// Return the current one.
	return vxlanid, nil

}

func isPairContainerAlive(podname string) string {

	targetKey := "/ratchet/association/" + podname + "/id"
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

func isPrimaryContainerAlive(podname string) (string, string, string, string) {

	targetKey := "/ratchet/association/" + podname + "/primaryname"
	respPrimaryName, err := kapi.Get(context.Background(), targetKey, nil)
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

		respVxlanID, _ := kapi.Get(context.Background(), "/ratchet/association/"+podname+"/vxlanid", nil)
		// above needs error handling.

		// and we need the pair ip.
		respPairIP, _ := kapi.Get(context.Background(), "/ratchet/association/"+podname+"/pairip", nil)

		// and we need the pair if.
		respPairIF, _ := kapi.Get(context.Background(), "/ratchet/association/"+podname+"/pairifname", nil)

		return respPrimaryName.Node.Value, respVxlanID.Node.Value, respPairIP.Node.Value, respPairIF.Node.Value

	}

	return "", "", "", ""

}

func pairWait(containerid string, linki LinkInfo) error {

	// Ok, we're not primary (we are the pair). So, go into a wait loop.
	// we need to find out when the primary finishes.
	// then we can create our vxlan, if need be.
	primarytries := 0

	var primaryname, primaryvxlanid, pairip, pairifname string

	for {

		primaryname, primaryvxlanid, pairip, pairifname = isPrimaryContainerAlive(linki.PodName)

		if len(primaryname) >= 1 {
			// We found it.
			logger(fmt.Sprintf("FOUND PRIMARY: %v", primaryname))
			break
		}

		primarytries++

		if debug {
			logger(fmt.Sprintf("Is PRIMARY alive? primaryname: %v (%v retries)", linki.PodName, primarytries))
		}

		// We either timeout, or, we're alive.
		if primarytries >= aliveWaitRetries {
			return fmt.Errorf("Timeout: could not find that PRIMARY container is alive via metadata in %v tries", primarytries)
		}

		// Wait for however long.
		time.Sleep(aliveWaitSeconds * time.Second)

	}

	_, primaryparentaddr, primaryparentinfoerr := getVxLanParentInfo(primaryname)
	if primaryparentinfoerr != nil {
		return primaryparentinfoerr
	}

	// Determine if we're going to use vxlan.
	// Which we do by comparing the vxlan parent addresses.
	pairusevxlan := false

	if linki.ParentAddr != primaryparentaddr {
		pairusevxlan = true
	}

	if pairusevxlan {

		// Alright, create a vxlan interface, w00t.

		// We need a veth generally.
		pairns, errpairns := koko.GetDockerContainerNS(containerid)
		if errpairns != nil {
			return fmt.Errorf("failed to get pairns (pair) %v: %v", containerid, errpairns)
		}

		ippair, maskpair, errpairparsecidr := net.ParseCIDR(pairip + "/24")
		if errpairparsecidr != nil {
			return fmt.Errorf("failed to parse IP (pairparse) %s: %v", linki.LocalIP+"/24", errpairparsecidr)
		}
		ipaddrpair := net.IPNet{
			IP:   ippair,
			Mask: maskpair.Mask,
		}

		vethpair := koko.VEth{}
		vethpair.NsName = pairns
		vethpair.IPAddr = append(vethpair.IPAddr, ipaddrpair)
		vethpair.LinkName = pairifname

		// Set the vxlan properties.
		vxlanpair := koko.VxLan{}
		vxlanpair.ParentIF = linki.ParentIface
		vxlanpair.IPAddr = net.ParseIP(primaryparentaddr)
		useprimaryvxlanid, _ := strconv.Atoi(primaryvxlanid)
		vxlanpair.ID = useprimaryvxlanid

		// Log it all.
		logger(fmt.Sprintf("(pair) VXLAN INFO: %v", vxlanpair))
		logger(fmt.Sprintf("(pair) VETH INFO: %v", vethpair))

		// Now ask koko to do it?
		errvxlan := koko.MakeVxLan(vethpair, vxlanpair)

		if errvxlan != nil {
			logger(fmt.Sprintf("(pair) VXLAN ERROR: %v", errvxlan))
			return errvxlan
		}

		logger("Koko VXLAN creation, success (pair)")

	}

	return nil

}

func primaryWait(linki LinkInfo) (string, error) {

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
			logger(fmt.Sprintf("Is pair alive? pair_name: %v (%v retries)", linki.PairName, tries))
		}

		// We either timeout, or, we're alive.
		if tries >= aliveWaitRetries {
			return "", fmt.Errorf("Timeout: could not find that pair container is alive via metadata in %v tries", tries)
		}

		// Wait for however long.
		time.Sleep(aliveWaitSeconds * time.Second)

	}

	return pairContainerID, nil

}

func ratchet(argif string, containerid string, linki LinkInfo) error {

	logger(fmt.Sprintf("ratchet LinkInfo: %v", linki))

	// If this is up, we can assume the infra container is good to go.
	// So all we need to do is associate our containerid with our name.
	vxlanid, _ := associateEtcdInfo(containerid, linki)

	// !bang
	// If it's determined that we're alive, now we can see if we're primary.
	// If we're not primary, we can just exit right now.
	// Cause the primary side will add to this pair.

	if linki.Primary != "true" {

		return pairWait(containerid, linki)

	}

	// Check to see there's a valid pair name.
	if len(linki.PairName) <= 1 {
		// That's not good.
		return fmt.Errorf("Pair name appears to be invalid: %v", linki.PairName)
	}

	// Now we want to check and see if the pair container is alive.
	// So it's time to go into a loop and do that.
	// if the pair container is alive -- bada bing, we can execute koko.

	pairContainerID, waiterror := primaryWait(linki)
	if waiterror != nil {
		return waiterror
	}

	// Now, we can probably rock out all the
	logger(fmt.Sprintf("And my pair's container id is: %v", pairContainerID))

	// What about a healthy delay?
	// TODO: This may or may not be necessary.
	logger(fmt.Sprintf("Pre koko-delay, %v SECONDS", delayKokoSeconds))
	time.Sleep(delayKokoSeconds * time.Second)

	// Let's pick up the pair's parent interface info.
	pairparentiface, pairparentaddr, parentinfoerr := getVxLanParentInfo(linki.PairName)
	if parentinfoerr != nil {
		return parentinfoerr
	}

	logger(fmt.Sprintf("Got parent info, OK: %v / %v", pairparentiface, pairparentaddr))

	// Ok, so now that we have the parent interface information for the pair...
	// We can now decide if we want to use vxlan.
	// For now we use the configured IP address in the config to determine if they're different.
	// TODO: This needs to be more dynamic, and use a better standard way of doing it.
	var usevxlan = false

	if linki.ParentAddr != pairparentaddr {
		// That'd be the time to use vxlan
		usevxlan = true
	}

	// Parse addr/cidr into net objects.
	ip1, mask1, err1 := net.ParseCIDR(linki.LocalIP + "/24")
	ip2, mask2, err2 := net.ParseCIDR(linki.PairIP + "/24")

	// Check those worked.
	if err1 != nil {
		return fmt.Errorf("failed to parse IP addr1 %s: %v", linki.LocalIP+"/24", err1)
	}

	if err2 != nil {
		return fmt.Errorf("failed to parse IP addr2 %s: %v", linki.PairIP+"/24", err1)
	}

	// Make a IPNet object for each of these.
	ipaddr1 := net.IPNet{
		IP:   ip1,
		Mask: mask1.Mask,
	}

	ipaddr2 := net.IPNet{
		IP:   ip2,
		Mask: mask2.Mask,
	}

	// Get the net namespaces
	ns1, err1 := koko.GetDockerContainerNS(containerid)
	if err1 != nil {
		return fmt.Errorf("failed to get containerns1 (primary) %v: %v", containerid, err1)
	}

	// And assign those to the initial veth data structure.
	veth1 := koko.VEth{}
	veth1.NsName = ns1
	veth1.IPAddr = append(veth1.IPAddr, ipaddr1)
	veth1.LinkName = linki.LocalIFName

	if usevxlan {

		// Ok, so we gotta create our own vxlan.
		// Then we have to somehow remotely trigger the other side to make a vxlan.

		// let's figure out our own, here first.

		// Pick up the vxlan id from etcd.

		// Set the vxlan properties.
		vxlan := koko.VxLan{}
		vxlan.ParentIF = linki.ParentIface
		vxlan.IPAddr = net.ParseIP(pairparentaddr)
		vxlan.ID = vxlanid

		// Log it all.
		logger(fmt.Sprintf("VXLAN INFO: %v", vxlan))

		// Now ask koko to do it?
		errvxlan := koko.MakeVxLan(veth1, vxlan)

		if errvxlan != nil {
			logger(fmt.Sprintf("VXLAN ERROR: %v", errvxlan))
			return errvxlan
		}

		logger("Koko VXLAN creation, success (primary)")

	} else {

		// Make a vEth
		// dump_my_meta := spew.Sdump(my_meta)
		// os.Stderr.WriteString("The containerid: " + containerid + "\n")
		// os.Stderr.WriteString("DOUG !trace my_meta ----------\n" + dump_my_meta)
		// os.Stderr.WriteString("DOUG !trace pair_alive ----------" + fmt.Sprintf("%t",pair_alive) + "\n")
		ns2, err2 := koko.GetDockerContainerNS(pairContainerID)
		if err2 != nil {
			return fmt.Errorf("failed to get containerns2 (pair) %v: %v", pairContainerID, err2)
		}

		// make the other side of the veth.
		veth2 := koko.VEth{}

		veth2.NsName = ns2
		veth2.IPAddr = append(veth2.IPAddr, ipaddr2)
		veth2.LinkName = linki.PairIFName

		kokoErr := koko.MakeVeth(veth1, veth2)

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

		logger("Koko VETH creation, success (primary)")

	}

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
	linki.ParentIface = os.Args[15]
	linki.ParentAddr = os.Args[16]

	err := ratchet(os.Args[1], os.Args[2], linki)
	if err != nil {
		logger("completition WITH ERROR")
		logger(fmt.Sprintf("%v", err))
	} else {
		logger("ratchet completition, success. (containerid: " + os.Args[2] + ")")
	}

}
