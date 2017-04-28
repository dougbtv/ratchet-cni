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
  // "time"
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
  // "github.com/coreos/etcd/client"
)

const ALIVE_WAIT_SECONDS = 1
const ALIVE_WAIT_RETRIES = 5
const defaultCNIDir = "/var/lib/cni/multus"
const DEBUG = true
const PERFORM_DELETE = false

var etcd_host string

var logger = log.New(os.Stderr, "", 0)

var masterpluginEnabled bool

type NetConf struct {
  types.NetConf
  CNIDir    string                   `json:"cniDir"`
  Delegate map[string]interface{}    `json:"delegate"`
  Etcd_host string                   `json:"etcd_host"`
  Use_labels bool                    `json:"use_labels"`
  Child_path string                  `json:"child_path"`
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

func printResults(delresult *types.Result) error {
  return delresult.Print()
}


func ratchet(netconf *NetConf,argif string,containerid string) error {

  var result error

  // Alright first few things:
  // 1. Here is where I'd add that we check the k8s api
  //    in order to see if there's a label that makes this applicable to ratcheting.
  //    other wise it'd just get the delegate.
  //    let's just delegate everything first, though.    

  // ------------------------ Delegate "boot network"
  // -- this interface defined the "delegate" section in the config
  // -- defines which interface is used for "regular network access"

  if err := checkDelegate(netconf.Delegate); err != nil {
    return fmt.Errorf("Multus: Err in delegate conf: %v", err)
  }

  podifName := getifname()
  // var mIndex int
  err, r := delegateAdd(podifName, argif, netconf.Delegate, false)
  if err != nil {
    panic(err)
  }

  // if err != true {
  //   // result = r
  //   // mIndex = index
  // } else if (err != false) && r != nil {
  //   // return r
  // }

  // ------------------------ Check label
  ctx := context.Background()
  cli, err_docker := dockerclient.NewEnvClient()
  if err_docker != nil {
    panic(err_docker)
  }

  cli.UpdateClientVersion("1.24")

  json, _ := cli.ContainerInspect(ctx, containerid)

  logger.Printf("DOUG !trace json >>>>>>>>>>>>>>>>>>>>>----------%v\n",json.Config.Labels)

  if _, use_ratchet := json.Config.Labels["ratchet"]; use_ratchet {
    logger.Println("USE RATCHET ----------------------->>>>>>>>>>>>>>>")
  } else {
    logger.Println("DO NOT USE RATCHET ---------------------<<<<<<<<<<<<<<<")
    if (netconf.Use_labels) {
      // When using labels, we're done, now.
      // So return...
      logger.Println("USING LABELS HERE ---------------------<<<<<<<<<<<<<<<")
      return printResults(r)
    } else {
      // When using not using labels (typically for development)
      // we continue along.
      logger.Println("NO NO NOT USING LABELS HERE ---------------------<<<<<<<<<<<<<<<")
    }
  }

  if (DEBUG) {
    
    // Docker inspect.
    dump_json := spew.Sdump(json)
    logger.Printf("DOUG !trace json ----------%v\n",dump_json)
  
    // dump_netconf := spew.Sdump(netconf)
    // os.Stderr.WriteString("DOUG !trace ----------\n" + dump_netconf)
    // os.Stderr.WriteString("Before sleep......................")
    // time.Sleep(10 * time.Second)
    // os.Stderr.WriteString("After sleep......................")
  }

  // Ok so we're continuing on -- can we fake out that we're done by printing?
  // ..not really, the pod is still not ready.
  printResults(r)

  // So we're going to try to spawn a process.
  // cmd_path := "/opt/cni/bin/test.sh"
  // cmd_path := "/home/centos/cni/bin/test.sh"
  // cmd_path := "./test.sh"

  // logger.Printf("executing path: %v / argif: %v / containerID: %v / etcd_host: %v",netconf.Child_path,argif,containerid,netconf.Etcd_host);
  // exec_string := netconf.Child_path + " " + argif + " " + containerid + " " + netconf.Etcd_host
  // logger.Printf("executing path composite: %v",exec_string);
  cmd := exec.Command(
    netconf.Child_path,
    argif,
    containerid,
    netconf.Etcd_host,
  )
  cmd.Start()

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
  rerr := ratchet(n,args.IfName,args.ContainerID)
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

func main() {
  

  if (DEBUG) {
    logger.Println("[LOGGING ENABLED]")
  }
  
  skel.PluginMain(cmdAdd, cmdDel)
}