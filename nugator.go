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

  "github.com/davecgh/go-spew/spew"
  "github.com/redhat-nfvpe/koko"
  "github.com/coreos/etcd/client"
)

const defaultCNIDir = "/var/lib/cni/multus"
const DEBUG = false
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

// vEth is a structure to descrive veth interfaces.
type announceData struct {
  Pod_name string
  Pair_name string
  Public_ip string
  Local_ip string
  Local_ifname string
  Pair_ip string
  Pair_ifname string
  Primary string
}

/*
pod_name
pair_name
public_ip
local_ip
local_ifname
pair_ip
pair_ifname
primary
*/

func getEtcdData(containerid string, setalive bool) (map[string]string) {

  all := make(map[string]string)

  // adata := announceData{}

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

      logger.Println(fmt.Errorf("possible missing value %s: %v", target_key, err))

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
    dump_netconf := spew.Sdump(netconf)
    koko.TestOne()
    os.Stderr.WriteString("DOUG !trace ----------\n" + dump_netconf)
    os.Stderr.WriteString("Before sleep......................")
    time.Sleep(10 * time.Second)
    os.Stderr.WriteString("After sleep......................")
  }

  // Keep out etcdhost around.
  etcd_host = netconf.Etcd_host

  // Go and pick up results from etcd.
  etcresult := getEtcdData("test123",true)

  dump_etcresult := spew.Sdump(etcresult)
  os.Stderr.WriteString("The containerid: " + containerid + "\n")
  os.Stderr.WriteString("DOUG !trace etcresult ----------\n" + dump_etcresult)
 

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
  ratchet(n,args.ContainerID)

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