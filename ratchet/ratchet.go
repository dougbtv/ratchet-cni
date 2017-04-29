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
  "time"
  "log"
  "io/ioutil"
  // "reflect"
  "os"
  "os/exec"
  "path/filepath"

  "github.com/containernetworking/cni/pkg/invoke"
  "github.com/containernetworking/cni/pkg/skel"
  "github.com/containernetworking/cni/pkg/types"
  "golang.org/x/net/context"

  dockerclient "github.com/docker/docker/client"
  "github.com/davecgh/go-spew/spew"
  // "github.com/redhat-nfvpe/koko"
  "github.com/coreos/etcd/client"
)

const ALIVE_WAIT_SECONDS = 1
const ALIVE_WAIT_RETRIES = 120
const defaultCNIDir = "/var/lib/cni/multus"
const DEBUG = true
const PERFORM_DELETE = false

var kapi client.KeysAPI

var logger = log.New(os.Stderr, "", 0)

var masterpluginEnabled bool

type NetConf struct {
  types.NetConf
  CNIDir    string                    `json:"cniDir"`
  Delegate map[string]interface{}     `json:"delegate"`
  Etcd_host string                    `json:"etcd_host"`
  Use_labels bool                     `json:"use_labels"`
  Child_path string                   `json:"child_path"`
  Boot_network map[string]interface{} `json:"boot_network"`
}

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

func delegateAdd(podif func() string, argif string, netconf map[string]interface{}, onlyMaster bool) (error, *types.Result) {
  netconfBytes, err := json.Marshal(netconf)
  if err != nil {
    return fmt.Errorf("Multus: error serializing multus delegate netconf: %v", err), nil
  }

  if os.Setenv("CNI_IFNAME", argif) != nil {
    return fmt.Errorf("Multus: error in setting CNI_IFNAME"), nil
  }
  
  result, err := invoke.DelegateAdd(netconf["type"].(string), netconfBytes)
  if err != nil {
    return fmt.Errorf("Multus: error in invoke Delegate add - %q: %v", netconf["type"].(string), err), nil
  }
  
  return nil, result
  
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
        logger.Println(fmt.Errorf("isPairContainerAlive - possible missing value %s: %v", target_key, err))
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

func printResults(delresult *types.Result) error {
  return delresult.Print()
}

func ratchet(netconf *NetConf,argif string,containerid string) error {

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

  if err := checkDelegate(netconf.Boot_network); err != nil {
    return fmt.Errorf("Ratchet Boot_network: Err in delegate conf: %v", err)
  }

  podifName := getifname()
  

  // ------------------------ Check label
  ctx := context.Background()
  cli, err_docker := dockerclient.NewEnvClient()
  if err_docker != nil {
    panic(err_docker)
  }

  cli.UpdateClientVersion("1.24")

  json, _ := cli.ContainerInspect(ctx, containerid)

  logger.Printf("DOUG !trace json >>>>>>>>>>>>>>>>>>>>>----------%v\n",json.Config.Labels)

  // ------------------------ Determine container eligibility.

  var err error
  var r *types.Result

  if _, use_ratchet := json.Config.Labels["ratchet"]; use_ratchet {

    logger.Println("USE RATCHET ----------------------->>>>>>>>>>>>>>>")
    // We want to use the ratchet "boot_network"
    err, r = delegateAdd(podifName, argif, netconf.Boot_network, false)
    if err != nil {
      logger.Printf("ratchet delegateAdd boot_network error ----------%v",err)
      return err
    }

  } else {

    // We used to switch here depending on the use_labels flag, which is seemingly obsolete.
    // But, now, we just use the delegate.
    // var mIndex int
    logger.Println("DO NOT USE RATCHET (passthrough)----------------------->>>>>>>>>>>>>>>")
    err, r = delegateAdd(podifName, argif, netconf.Delegate, false)
    if err != nil {
      logger.Printf("ratchet delegateAdd passthrough error ----------%v",err)
      return err
    }

    return printResults(r)
    
  }

  // If you get to this point -- you're eligible for treatment under ratchet.

  // Populate all the possible link info.

  linki := LinkInfo{}
  linki.Pod_name = json.Config.Labels["ratchet.pod_name"]
  linki.Target_pod = json.Config.Labels["ratchet.target_pod"]
  linki.Target_container = json.Config.Labels["ratchet.target_container"]
  linki.Public_ip = json.Config.Labels["ratchet.public_ip"]
  linki.Local_ip = json.Config.Labels["ratchet.local_ip"]
  linki.Local_ifname = json.Config.Labels["ratchet.local_ifname"]
  linki.Pair_name = json.Config.Labels["ratchet.pair_name"]
  linki.Pair_ip = json.Config.Labels["ratchet.pair_ip"]
  linki.Pair_ifname = json.Config.Labels["ratchet.pair_ifname"]
  linki.Primary = json.Config.Labels["ratchet.primary"]

  dump_linki := spew.Sdump(linki)
  logger.Printf("DOUG !trace linki ----------%v\n",dump_linki)

  // Spawn external process.
  // ...pass tons of link info along with some basics.

  // logger.Printf("executing path: %v / argif: %v / containerID: %v / etcd_host: %v",netconf.Child_path,argif,containerid,netconf.Etcd_host);
  // exec_string := netconf.Child_path + " " + argif + " " + containerid + " " + netconf.Etcd_host
  // logger.Printf("executing path composite: %v",exec_string);
  cmd := exec.Command(
    netconf.Child_path,       // 0
    argif,                    // 1
    containerid,
    netconf.Etcd_host,
    linki.Pod_name,
    linki.Target_pod,
    linki.Target_container,
    linki.Public_ip,
    linki.Local_ip,
    linki.Local_ifname,
    linki.Pair_name,
    linki.Pair_ip,
    linki.Pair_ifname,
    linki.Primary,
  )
  cmd.Start()

  return printResults(r)

}

func cmdAdd(args *skel.CmdArgs) error {

  var result error
  n, err := loadNetConf(args.StdinData)
  if err != nil {
    return err
  }

  // Pass a pointer to the NetConf type.
  // logger.Println(reflect.TypeOf(n))
  rerr := ratchet(n,args.IfName,args.ContainerID)
  if rerr != nil {
    logger.Printf("Ratchet error from cmdAdd handler: %v",rerr)
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
    result = ratchet(in,args.IfName,args.ContainerID)
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
  
  if (DEBUG) {
    logger.Println("[LOGGING ENABLED]")
  }
  
  skel.PluginMain(cmdAdd, cmdDel)
}