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
  "time"
  "log"
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
  "github.com/redhat-nfvpe/koko"
  "github.com/coreos/etcd/client"
)

const ALIVE_WAIT_SECONDS = 1
const ALIVE_WAIT_RETRIES = 60
const DELAY_KOKO_SECONDS = 1
const defaultCNIDir = "/var/lib/cni/multus"
const DEBUG = true
const PERFORM_DELETE = false

var kapi client.KeysAPI

var masterpluginEnabled bool

type LinkInfo struct {
  Pod_name string
  Target_pod string
  Target_container string
  Public_ip string
  Local_ip string
  Local_ifname string
  Pair_name string
  Pair_ip string
  Pair_ifname string
  Primary string
}

func isContainerAlive(containername string) bool {
  isalive := false

  target_key := "/ratchet/byname/" + containername
  _, err := kapi.Get(context.Background(), target_key, nil)
  if err != nil {

      // ErrorCodeKeyNotFound = Key not found, that's exactly the one we know is good.
      // So let's log when it's not that.
      // Passing along on this.
      /*
      if (err != client.ErrorCodeKeyNotFound) {
        logger.Println(fmt.Errorf("isContainerAlive - possible missing value %s: %v", target_key, err))
      }
      */

    } else {
      // no error? must be there.
      isalive = true
    }

  return isalive

}

func getContainerIDByName(containername string) (error, string) {
  
  target_key := "/ratchet/byname/" + containername
  resp_containerid, err := kapi.Get(context.Background(), target_key, nil)
  if err != nil {

      return fmt.Errorf("Error picking up container id by name: %v",containername), ""

    } else {
      return nil, resp_containerid.Node.Value;
    }

}

func amIAlive(containerid string) bool {
  isalive := false

  target_key := "/ratchet/" + containerid + "/pod_name"
  _, err := kapi.Get(context.Background(), target_key, nil)
  if err != nil {

      // ErrorCodeKeyNotFound = Key not found, that's exactly the one we know is good.
      // So let's log when it's not that.
      // Passing along on this.
      /*
      if (err != client.ErrorCodeKeyNotFound) {
        logger.Println(fmt.Errorf("isContainerAlive - possible missing value %s: %v", target_key, err))
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
func getEtcdMetaData(containerid string, setalive bool) (map[string]string) {

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

  for k, _ := range all { 
    // Print all possibilities...
    // logger(fmt.Sprintf("key[%s] value[%s]\n", k, v))

    // get a key's value
    // logger.Print("Getting '/ratchet/" + containerid + "/" + k + "' key value")
    getcfg := &client.GetOptions{Recursive: true}
    target_key := "/ratchet/" + containerid + "/" + k
    message_resp, err := kapi.Get(context.Background(), target_key, getcfg)
    if err != nil {

      // For now, this seems to be just the missing values.
      // ...which are generally fine.
      // logger.Println(fmt.Errorf("possible missing value %s: %v", target_key, err))

    } else {
      // print common key info
      // logger(fmt.Sprintf("Get is done. Metadata is %q\n", message_resp))
      // print value

      // dump_resp := spew.Sdump(message_resp)
      // os.Stderr.WriteString("DOUG !trace message_resp ----------\n" + dump_resp)

      // dump_adata := spew.Sdump(adata)
      // os.Stderr.WriteString("DOUG !trace adata ----------\n" + dump_adata)

      all[k] = message_resp.Node.Value;

      // logger(fmt.Sprintf("%q key has %q value\n", message_resp.Node.Key, message_resp.Node.Value))
    }


  }

  if (setalive) {

    // set "/foo" key with "bar" value
    // log.Print("Setting '/foo' key with 'bar' value")
    _, err := kapi.Set(context.Background(), "/ratchet/" + containerid + "/isalive", "true", nil)
    if err != nil {
      log.Fatal(err)
    } else {
      // print common key info
      // log.Printf("Set is done. Metadata is %q\n", resp)
    }

  }

  return all

}

func associateIDEtcd (containerid string,podname string) error {

  // set "/foo" key with "bar" value
  // log.Print("Setting '/foo' key with 'bar' value")
  _, err := kapi.Set(context.Background(), "/ratchet/association/" + podname, containerid, nil)
  if err != nil {
    log.Fatal(err)
    return err
  } else {
    // print common key info
    // log.Printf("Set is done. Metadata is %q\n", resp)
  }

  return nil

}

func isPairContainerAlive(podname string) string {
  
  target_key := "/ratchet/association/" + podname
  resp_containerid, err := kapi.Get(context.Background(), target_key, nil)
  if err != nil {

      // ErrorCodeKeyNotFound = Key not found, that's exactly the one we know is good.
      // So let's log when it's not that.
      // Passing along on this.
      /*
      if (err != client.ErrorCodeKeyNotFound) {
        logger.Println(fmt.Errorf("isPairContainerAlive - possible missing value %s: %v", target_key, err))
      }
      */

    } else {
      // no error? must be there.
      return resp_containerid.Node.Value;
    }

  return ""

}

func ratchet(argif string,containerid string, linki LinkInfo) error {

  os.Stderr.WriteString("!trace alive The containerid: " + containerid + "\n")

  // We no longer care if we're alive anymore. 
  // If this is up, we can assume the infra container is good to go.
  // So all we need to do is associate our containerid with our name.
  associateIDEtcd(containerid,linki.Pod_name)

  // If it's determined that we're alive, now we can see if we're primary.
  // If we're not primary, we can just exit right now.
  // Cause the primary side will add to this pair.

  if (linki.Primary != "true") {
    // Ok, we're not primary. So... time to exit.
    if (DEBUG) {
      logger(fmt.Sprintf("Normal termination, this container is not primary (name: %v, containerid: %v, primary: %v)",linki.Pod_name,containerid,linki.Primary))
    }

    return nil
  }

  // Check to see there's a valid pair name.
  if (len(linki.Pair_name) <= 1) {
    // That's not good.
    return fmt.Errorf("Pair name appears to be invalid: %v", linki.Pair_name)
  }

  // Now we want to check and see if the pair container is alive.
  // So it's time to go into a loop and do that.
  // if the pair container is alive -- bada bing, we can execute koko.

  var pair_containerid string
  tries := 0

  for {
    
    pair_containerid = isPairContainerAlive(linki.Pair_name)

    if (len(pair_containerid) >= 1) {
      // We found it.
      break;
    }

    tries++

    if (DEBUG) {
      logger(fmt.Sprintf("Is pair alive? pair_name: %v (%v retries)\n",linki.Pair_name,tries))
    }

    // We either timeout, or, we're alive.
    if (tries >= ALIVE_WAIT_RETRIES) {
      return fmt.Errorf("Timeout: could not find that pair container is alive via metadata in %v tries", tries)
    }

    // Wait for however long.
    time.Sleep(ALIVE_WAIT_SECONDS * time.Second)

  }

  // Now, we can probably rock out all the 
  logger(fmt.Sprintf("And my pair's container id is: %v",pair_containerid))

  // dump_my_meta := spew.Sdump(my_meta)
  // os.Stderr.WriteString("The containerid: " + containerid + "\n")
  // os.Stderr.WriteString("DOUG !trace my_meta ----------\n" + dump_my_meta)
  // os.Stderr.WriteString("DOUG !trace pair_alive ----------" + fmt.Sprintf("%t",pair_alive) + "\n")

  // !trace !bang
  // This is how you call up koko.
  // koko.VethCreator("foo","192.168.2.100/24","in1","bar","192.168.2.101/24","in2")

  // What about a healthy delay?
  logger("Pre koko-delay, " + DELAY_KOKO_SECONDS + " SECONDS")
  time.Sleep(DELAY_KOKO_SECONDS * time.Second)


  koko_err := koko.VethCreator(
    containerid,
    linki.Local_ip + "/24",
    linki.Local_ifname,
    pair_containerid,
    linki.Pair_ip + "/24",
    linki.Pair_ifname,
  )

  if (koko_err != nil) {
    logger(fmt.Sprintf("koko error in child: %v",koko_err))
    return koko_err
  }

  logger("Koko appears to have completed with success.")

  return nil

}

func logger(input string) {

  // exec_command := 
  // os.Stderr.WriteString("!trace alive The containerid: |" + exec_command + "|||\n")
  cmd := exec.Command("/bin/bash", "-c", "echo \"ratchet-child: " + input + "\" | systemd-cat")
  cmd.Start()

}


func initEtcd(etcd_host string) {

  // Make a connection to etcd. Then we reuse the "kapi"

  cfg := client.Config{
    Endpoints:               []string{"http://" + etcd_host + ":2379"},
    Transport:               client.DefaultTransport,
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

  initEtcd(os.Args[3])

  if (DEBUG) {
    logger("[LOGGING ENABLED]")
    logger(fmt.Sprintf("Interface: %v",os.Args[1]))
    logger(fmt.Sprintf("ContainerID: %v",os.Args[2]))
    // Inspect all the arguments.
    for idx,element := range os.Args {
      logger(fmt.Sprintf("arg[%v]: %v",idx,element))
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
  linki.Pod_name = os.Args[4]
  linki.Target_pod = os.Args[5]
  linki.Target_container = os.Args[6]
  linki.Public_ip = os.Args[7]
  linki.Local_ip = os.Args[8]
  linki.Local_ifname = os.Args[9]
  linki.Pair_name = os.Args[10]
  linki.Pair_ip = os.Args[11]
  linki.Pair_ifname = os.Args[12]
  linki.Primary = os.Args[13]

  err := ratchet(os.Args[1],os.Args[2],linki)
  if (err != nil) {
    logger("completition WITH ERROR")
    logger(fmt.Sprintf("%v",err))
  } else {
    logger("ratchet completition, success. (containerid: " + os.Args[2] + ")")
  }
  
}