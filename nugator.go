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
// This is Doug learning how to create a CNI plugin.
// nugator is latin for "pipsqueak"

package main

import (
  "encoding/json"
  "fmt"
  "time"
  "log"
  "io/ioutil"
  // "reflect"
  "os"
  "path/filepath"

  "github.com/containernetworking/cni/pkg/invoke"
  "github.com/containernetworking/cni/pkg/skel"
  "github.com/containernetworking/cni/pkg/types"
  "golang.org/x/net/context"

  // "github.com/davecgh/go-spew/spew"
  "github.com/redhat-nfvpe/koko"
  "github.com/coreos/etcd/client"
)

const ALIVE_WAIT_SECONDS = 1
const ALIVE_WAIT_RETRIES = 120
const defaultCNIDir = "/var/lib/cni/multus"
const DEBUG = true
const PERFORM_DELETE = false

var etcd_host string

var kapi client.KeysAPI

var logger = log.New(os.Stderr, "", 0)

var masterpluginEnabled bool

type NetConf struct {
  types.NetConf
  CNIDir    string                   `json:"cniDir"`
  Delegates []map[string]interface{} `json:"delegates"`
  Etcd_host string                   `json:"etcd_host"`
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

  if netconf.Delegates == nil {
    return nil, fmt.Errorf(`"delegates" is must, refer README.md`)
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

func delegateAdd(podif func() string, argif string, netconf map[string]interface{}, onlyMaster bool) (bool, error) {
  netconfBytes, err := json.Marshal(netconf)
  if err != nil {
    return true, fmt.Errorf("Multus: error serializing multus delegate netconf: %v", err)
  }

  if isMasterplugin(netconf) != onlyMaster {
    return true, nil
  }

  if !isMasterplugin(netconf) {
    if os.Setenv("CNI_IFNAME", podif()) != nil {
      return true, fmt.Errorf("Multus: error in setting CNI_IFNAME")
    }
  } else {
    if os.Setenv("CNI_IFNAME", argif) != nil {
      return true, fmt.Errorf("Multus: error in setting CNI_IFNAME")
    }
  }

  result, err := invoke.DelegateAdd(netconf["type"].(string), netconfBytes)
  if err != nil {
    return true, fmt.Errorf("Multus: error in invoke Delegate add - %q: %v", netconf["type"].(string), err)
  }

  if !isMasterplugin(netconf) {
    return true, nil
  }

  return false, result.Print()
}

func delegateDel(podif func() string, argif string, netconf map[string]interface{}) error {
  netconfBytes, err := json.Marshal(netconf)
  if err != nil {
    return fmt.Errorf("Multus: error serializing multus delegate netconf: %v", err)
  }

  if !isMasterplugin(netconf) {
    if os.Setenv("CNI_IFNAME", podif()) != nil {
      return fmt.Errorf("Multus: error in setting CNI_IFNAME")
    }
  } else {
    if os.Setenv("CNI_IFNAME", argif) != nil {
      return fmt.Errorf("Multus: error in setting CNI_IFNAME")
    }
  }

  err = invoke.DelegateDel(netconf["type"].(string), netconfBytes)
  if err != nil {
    return fmt.Errorf("Multus: error in invoke Delegate del - %q: %v", netconf["type"].(string), err)
  }

  return err
}

func clearPlugins(mIdx int, pIdx int, argIfname string, delegates []map[string]interface{}) error {

  if os.Setenv("CNI_COMMAND", "DEL") != nil {
    return fmt.Errorf("Multus: error in setting CNI_COMMAND to DEL")
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
    // logger.Printf("key[%s] value[%s]\n", k, v)

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
      // logger.Printf("Get is done. Metadata is %q\n", message_resp)
      // print value

      // dump_resp := spew.Sdump(message_resp)
      // os.Stderr.WriteString("DOUG !trace message_resp ----------\n" + dump_resp)

      // dump_adata := spew.Sdump(adata)
      // os.Stderr.WriteString("DOUG !trace adata ----------\n" + dump_adata)

      all[k] = message_resp.Node.Value;

      // logger.Printf("%q key has %q value\n", message_resp.Node.Key, message_resp.Node.Value)
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


func ratchet(netconf *NetConf,containerid string) error {

  var result error

  if (DEBUG) {
    koko.TestOne()
    // dump_netconf := spew.Sdump(netconf)
    // os.Stderr.WriteString("DOUG !trace ----------\n" + dump_netconf)
    // os.Stderr.WriteString("Before sleep......................")
    // time.Sleep(10 * time.Second)
    // os.Stderr.WriteString("After sleep......................")
  }

  // Keep out etcdhost around.
  etcd_host = netconf.Etcd_host

  // Go into a loop determining if we're alive 
  tries := 0
  i_am_alive := false
  for amIAlive(containerid) == false {
    
    tries++

    if (DEBUG) {
      logger.Printf("Am I alive? containerid: %v (%v retries)\n",containerid,tries);
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
      logger.Printf("Normal termination, this container is not primary (name: %v, containerid: %v, primary: %v)",my_meta["pod_name"],containerid,my_meta["primary"]);
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
      logger.Printf("Is pair alive? pair_name: %v (%v retries)\n",my_meta["pair_name"],tries);
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

  logger.Printf("And my pair's container id is: %v",pair_containerid)

  // dump_my_meta := spew.Sdump(my_meta)
  // os.Stderr.WriteString("The containerid: " + containerid + "\n")
  // os.Stderr.WriteString("DOUG !trace my_meta ----------\n" + dump_my_meta)
  // os.Stderr.WriteString("DOUG !trace pair_alive ----------" + fmt.Sprintf("%t",pair_alive) + "\n")

  // !trace !bang
  // This is how you call up koko.
  // koko.VethCreator("foo","192.168.2.100/24","in1","bar","192.168.2.101/24","in2")

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
  rerr := ratchet(n,args.ContainerID)
  if rerr != nil {
    return rerr
  }


  // for _, delegate := range n.Delegates {
  //   if err := checkDelegate(delegate); err != nil {
  //     return fmt.Errorf("Multus: Err in delegate conf: %v", err)
  //   }
  // }

  // // dump_args := spew.Sdump(args)
  // // os.Stderr.WriteString("DOUG !trace ----------\n" + dump_args)


  // if err := saveDelegates(args.ContainerID, n.CNIDir, n.Delegates); err != nil {
  //   return fmt.Errorf("Multus: Err in saving the delegates: %v", err)
  // }

  // podifName := getifname()
  // var mIndex int
  // for index, delegate := range n.Delegates {
  //   err, r := delegateAdd(podifName, args.IfName, delegate, true)
  //   if err != true {
  //     result = r
  //     mIndex = index
  //   } else if (err != false) && r != nil {
  //     return r
  //   }
  // }

  // for index, delegate := range n.Delegates {
  //   err, r := delegateAdd(podifName, args.IfName, delegate, false)
  //   if err != true {
  //     result = r
  //   } else if (err != false) && r != nil {
  //     perr := clearPlugins(mIndex, index, args.IfName, n.Delegates)
  //     if perr != nil {
  //       return perr
  //     }
  //     return r
  //   }
  // }

  // return result

  return result

}

func cmdDel(args *skel.CmdArgs) error {
  var result error
  in, err := loadNetConf(args.StdinData)
  if err != nil {
    return err
  }

  if (PERFORM_DELETE) {
    ratchet(in,args.ContainerID)
  }

  // netconfBytes, err := consumeScratchNetConf(args.ContainerID, in.CNIDir)
  // if err != nil {
  //   return fmt.Errorf("Multus: Err in  reading the delegates: %v", err)
  // }

  // var Delegates []map[string]interface{}
  // if err := json.Unmarshal(netconfBytes, &Delegates); err != nil {
  //   return fmt.Errorf("Multus: failed to load netconf: %v", err)
  // }

  // podifName := getifname()
  // for _, delegate := range Delegates {
  //   r := delegateDel(podifName, args.IfName, delegate)
  //   if r != nil {
  //     return r
  //   }
  //   result = r
  // }

  return result
}

func versionInfo(args *skel.CmdArgs) error {

  var result error
  fmt.Fprintln(os.Stderr, "Version v0.0.0")
  return result

}

func initEtcd() {

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
  // logger := 
  if (DEBUG) {
    logger.Println("[LOGGING ENABLED]")
  }
  initEtcd()
  skel.PluginMain(cmdAdd, cmdDel)
}