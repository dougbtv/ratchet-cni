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
const defaultCNIDir = "/var/lib/cni/multus"
const DEBUG = true
const PERFORM_DELETE = false

var kapi client.KeysAPI

var masterpluginEnabled bool

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

func ratchet(argif string,containerid string) error {

  os.Stderr.WriteString("!trace alive The containerid: " + containerid + "\n")

  var result error

  // Go into a loop determining if we're alive 
  tries := 0
  i_am_alive := false
  for amIAlive(containerid) == false {
    
    tries++

    if (DEBUG) {
      logger(fmt.Sprintf("Am I alive? containerid: %v (%v retries)\n",containerid,tries))
    }

    // We either timeout, or, we're alive.
    if (tries >= ALIVE_WAIT_RETRIES || i_am_alive) {
      return fmt.Errorf("Timeout: could not find this container is alive via metadata in %v tries", tries)
    }

    // Wait for however long.
    time.Sleep(ALIVE_WAIT_SECONDS * time.Second)
  }

  // If it's determined that we're alive, now we can see if we're primary.
  // If we're not primary, we can just exit right now.
  // Cause the primary side will add to this pair.

  // Go and pick up metadata about me from etcd.
  my_meta := getEtcdMetaData(containerid,true)

  if (my_meta["primary"] != "true") {
    // Ok, we're not primary. So... time to exit.
    if (DEBUG) {
      logger(fmt.Sprintf("Normal termination, this container is not primary (name: %v, containerid: %v, primary: %v)",my_meta["pod_name"],containerid,my_meta["primary"]))
    }

    return nil
  }

  // Check to see there's a valid pair name.
  if (len(my_meta["pair_name"]) <= 1) {
    // That's not good.
    return fmt.Errorf("Pair name appears to be invalid: %v", my_meta["pair_name"])
  }

  // Now we want to check and see if the pair container is alive.
  // So it's time to go into a loop and do that.
  // if the pair container is alive -- bada bing, we can execute koko.

  for isContainerAlive(my_meta["pair_name"]) == false {
    
    tries++

    if (DEBUG) {
      logger(fmt.Sprintf("Is pair alive? pair_name: %v (%v retries)\n",my_meta["pair_name"],tries))
    }

    // We either timeout, or, we're alive.
    if (tries >= ALIVE_WAIT_RETRIES || i_am_alive) {
      return fmt.Errorf("Timeout: could not find that pair container is alive via metadata in %v tries", tries)
    }

    // Wait for however long.
    time.Sleep(ALIVE_WAIT_SECONDS * time.Second)

  }

  // Alright, given that... we should have a valid pair.
  // So let's pick up the pair container id.
  pairid_err, pair_containerid := getContainerIDByName(my_meta["pair_name"])
  if (pairid_err != nil) {
    return pairid_err
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
  koko_err := koko.VethCreator(containerid,my_meta["local_ip"],my_meta["local_ifname"],pair_containerid,my_meta["pair_ip"],my_meta["pair_ifname"])
  if (koko_err != nil) {
    return koko_err
  }

  return result

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
  }

  err := ratchet(os.Args[1],os.Args[2])
  if (err != nil) {
    logger(fmt.Sprintf("%v",err))
  }
  
}