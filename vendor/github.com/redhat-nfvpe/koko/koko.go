/**
 * koko: Container connector
 */
package koko

import (
	"fmt"
	"github.com/mattn/go-getopt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/containernetworking/cni/pkg/ns"
	"github.com/vishvananda/netlink"

	"github.com/docker/docker/client"
	"golang.org/x/net/context"

	"github.com/MakeNowJust/heredoc"
	"github.com/davecgh/go-spew/spew"
)

func TestOne() {

	os.Stderr.WriteString("Hello from koko......................\n")
	return

}

func makeVethPair(name, peer string, mtu int) (netlink.Link, error) {
	veth := &netlink.Veth{
		LinkAttrs: netlink.LinkAttrs{
			Name:  name,
			Flags: net.FlagUp,
			MTU:   mtu,
		},
		PeerName: peer,
	}

	if err := netlink.LinkAdd(veth); err != nil {
		return nil, err
	}
	return veth, nil
}

func getVethPair(name1 string, name2 string) (link1 netlink.Link, link2 netlink.Link, err error) {

	link1, err = makeVethPair(name1, name2, 1500)
	if err != nil {
		switch {
		case os.IsExist(err):
			err = fmt.Errorf("container veth name provided (%v) already exists", name1)
			return
		default:
			err = fmt.Errorf("failed to make veth pair: %v", err)
			return
		}
	}

	link2, err = netlink.LinkByName(name2)
	if err != nil {
		logger(fmt.Sprintf("Failed to lookup %q: %v\n", name2, err))
	}

	return
}

// addVxLanInterface creates VxLan interface by given vxlan object
func addVxLanInterface(vxlan vxLan, devName string) error {
	parentIF, err := netlink.LinkByName(vxlan.parentIF)

	if err != nil {
		return fmt.Errorf("Failed to get %s: %v", vxlan.parentIF, err)
	}

	vxlanconf := netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{
				Name:		devName,
				TxQLen:		1000,
		},
		VxlanId:	vxlan.id,
		VtepDevIndex:	parentIF.Attrs().Index,
		Group:		vxlan.ipAddr,
		Port:		4789,
		Learning:	true,
		L2miss:		true,
		L3miss:		true,
	}
	err = netlink.LinkAdd(&vxlanconf)

	if err != nil {
		return fmt.Errorf("Failed to add vxlan %s: %v", devName, err)
	}
	return nil
}

// getDockerContainerNS retrieves container's network namespace from
// docker container id, given as containerID.
func getDockerContainerNS(containerID string) (namespace string, err error) {
	ctx := context.Background()
	cli, err := client.NewEnvClient()
	if err != nil {
		panic(err)
	}

	cli.UpdateClientVersion("1.24")

	json, err := cli.ContainerInspect(ctx, containerID)
	if err != nil {
		err = fmt.Errorf("failed to get container info: %v", err)
		return
	}
	if json.NetworkSettings == nil {
		err = fmt.Errorf("failed to get container info: %v", err)
		return
	}
	namespace = json.NetworkSettings.NetworkSettingsBase.SandboxKey
	return
}

// vEth is a structure to describe veth interfaces.
type vEth struct {
	nsName		string		// What's the network namespace?
	linkName	string		// And what will we call the link.
	withIPAddr	bool		// Is there an ip address?
	ipAddr		net.IPNet	// What is that ip address.
}

// vxLan is a structure to describe vxlan endpoint.
type vxLan struct {
	parentIF	string		// parent interface name
	id		int		// VxLan ID
	ipAddr		net.IP		// VxLan destination address
}

// setVethLink is low-level handler to set IP address onveth links given a single vEth data object.
// ...primarily used privately by makeVeth().
func (veth *vEth) setVethLink(link netlink.Link) (err error) {
	vethNs, err := ns.GetNS(veth.nsName)

	if err != nil {
		logger("setVethLink a: " + fmt.Sprintf("%v", err))
	}
	defer vethNs.Close()

	if err := netlink.LinkSetNsFd(link, int(vethNs.Fd())); err != nil {
		logger("setVethLink b: " + fmt.Sprintf("%v", err))
	}

	err = vethNs.Do(func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(veth.linkName)
		if err != nil {
			return fmt.Errorf("failed to lookup %q in %q: %v", veth.linkName, vethNs.Path(), err)
		}

		if err = netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("failed to set %q up: %v", veth.linkName, err)
		}

		// Conditionally set the IP address.
		if veth.withIPAddr {
			addr := &netlink.Addr{IPNet: &veth.ipAddr, Label: ""}
			if err = netlink.AddrAdd(link, addr); err != nil {
				return fmt.Errorf("failed to add IP addr %v to %q: %v", addr, veth.linkName, err)
			}
		}

		return nil
	})

	return
}

// removeVethLink is low-level handler to get interface handle in container/netns namespace and remove it.
func (veth *vEth) removeVethLink() (err error) {
	vethNs, err := ns.GetNS(veth.nsName)

	if err != nil {
		logger(fmt.Sprintf("%v", err))
	}
	defer vethNs.Close()

	err = vethNs.Do(func(_ ns.NetNS) error {
		link, err := netlink.LinkByName(veth.linkName)
		if err != nil {
			return fmt.Errorf("failed to lookup %q in %q: %v", veth.linkName, vethNs.Path(), err)
		}

		err = netlink.LinkDel(link)
		if err != nil {
			return fmt.Errorf("failed to remove link %q in %q: %v", veth.linkName, vethNs.Path(), err)
		}
		return nil
	})

	return
}


// makeVeth is top-level handler to create veth links given two vEth data objects: veth1 and veth2.
func makeVeth(veth1 vEth, veth2 vEth) {

	link1, link2, err := getVethPair(veth1.linkName, veth2.linkName)
	if err != nil {
		logger("makeVeth a:" + fmt.Sprintf("%v", err))
	}

	veth1.setVethLink(link1)
	veth2.setVethLink(link2)
}

// makeVxLan makes vxlan interface and put it into container namespace
func makeVxLan(veth1 vEth, vxlan vxLan) {

	err := addVxLanInterface(vxlan, veth1.linkName)
	if err != nil {
		logger("makeVxlan b:" + fmt.Sprintf("vxlan add failed: %v", err))
	}

	link, err2 := netlink.LinkByName(veth1.linkName)
	if err2 != nil {
		logger("makeVxlan c:" + fmt.Sprintf("Cannot get %s: %v", veth1.linkName, err))
	}
	err = veth1.setVethLink(link)
	if err != nil {
		logger("makeVxlan d:" + fmt.Sprintf("Cannot add IPaddr/netns failed: %v", err))
	}
}

// parseNOption parses '-n' option and put this information in veth object.
func parseNOption(s string) (veth vEth, err error) {
	n := strings.Split(s, ":")
	if len(n) != 3 && len(n) != 2 {
		err = fmt.Errorf("failed to parse %s", s)
		return
	}

	veth.nsName = fmt.Sprintf("/var/run/netns/%s", n[0])
	if err != nil {
		logger(fmt.Sprintf("%v", err))
	}

	veth.linkName = n[1]

	if len(n) == 3 {
		ip, mask, err2 := net.ParseCIDR(n[2])
		if err2 != nil {
			err = fmt.Errorf("failed to parse IP addr %s: %v",
				n[2], err2)
			return
		}
		veth.ipAddr.IP = ip
		veth.ipAddr.Mask = mask.Mask
		veth.withIPAddr = true
	} else {
		veth.withIPAddr = false
	}

	return
}

// parseNOption parses '-n' option and put this information in veth object.
func parseDOption(s string) (veth vEth, err error) {
	n := strings.Split(s, ":")
	if len(n) != 3 && len(n) != 2 {
		err = fmt.Errorf("failed to parse %s", s)
		return
	}

	veth.nsName, err = getDockerContainerNS(n[0])
	if err != nil {
		logger(fmt.Sprintf("%v", err))
	}

	veth.linkName = n[1]

	if len(n) == 3 {
		ip, mask, err2 := net.ParseCIDR(n[2])
		if err2 != nil {
			err = fmt.Errorf("failed to parse IP addr %s: %v",
				n[2], err2)
			return
		}
		veth.ipAddr.IP = ip
		veth.ipAddr.Mask = mask.Mask
		veth.withIPAddr = true
	} else {
		veth.withIPAddr = false
	}

	return
}

// parseXOption parses '-x' option and put this information in veth object.
func parseXOption(s string) (vxlan vxLan, err error) {
	var err2 error // if we encounter an error, it's marked here.

	n := strings.Split(s, ":")
	if len(n) != 3 {
		err = fmt.Errorf("failed to parse %s", s)
		return
	}

	vxlan.parentIF = n[0]
	vxlan.ipAddr = net.ParseIP(n[1])
	vxlan.id, err2 = strconv.Atoi(n[2])
	if err2 != nil {
		err = fmt.Errorf("failed to parse VXID %s: %v", n[2], err2)
		return
	}

	return
}

// usage shows usage when user invokes it with '-h' option.
func usage() {
	doc := heredoc.Doc(`
		
		Usage:
		./koko -d centos1:link1:192.168.1.1/24 -d centos2:link2:192.168.1.2/24 #with IP addr
		./koko -d centos1:link1 -d centos2:link2  #without IP addr
		./koko -n /var/run/netns/test1:link1:192.168.1.1/24 <other>	
	`)

	fmt.Print(doc)

}

const LOG_OUTPUT = false

func logger(input string) {

	if (LOG_OUTPUT) {
	  // exec_command := 
	  // os.Stderr.WriteString("!trace alive The containerid: |" + exec_command + "|||\n")
	  cmd := exec.Command("/bin/bash", "-c", "echo \"ratchet-child: " + input + "\" | systemd-cat")
	  cmd.Start()
	}

}

func getProcessNamespace(containerid string) (error,string) {

	// Pick up the pid from docker inspect.
	// Template the path to that

	cli, err := client.NewEnvClient()
	if err != nil {
		err = fmt.Errorf("failed to do NewEnvClient: %v", err)
		return err, ""
	}

	cli.UpdateClientVersion("1.24")

	json, err := cli.ContainerInspect(context.Background(), containerid)
	if err != nil {
		err = fmt.Errorf("failed to get container info: %v", err)
		return err, ""
	}

	/*

	This would be nice to check, but, I'm not sure what to look for.

	if json.State.Pid == nil {
		err = fmt.Errorf("failed to get container info: %v", err)
		return err, ""
	}
	*/

	return nil, "/proc/" + strconv.Itoa(json.State.Pid) + "/ns/net"

}

func VethCreator (local_container string, local_ipnetmask string, local_ifname string, pair_container string, pair_ipnetmask string, pair_ifname string) (err error) {

	vethA := vEth{}
	vethB := vEth{}

	// Get the pieces for vethA ----------------------------------

	err, vethA.nsName = getProcessNamespace(local_container)
	// logger(fmt.Sprintf("vethA.nsName: %v",vethA.nsName))
	if err != nil {
		err = fmt.Errorf("Failure to get docker container ns (vetha) -- %v: %v", pair_container, err)
		return err
	}

	ip, mask, err := net.ParseCIDR(local_ipnetmask)
	if err != nil {
		err = fmt.Errorf("failed to parse IP addr %s: %v", local_ipnetmask, err)
		return err
	}

	vethA.linkName = local_ifname
	vethA.ipAddr.IP = ip
	vethA.ipAddr.Mask = mask.Mask
	vethA.withIPAddr = true

	// Get the pieces for vethB ----------------------------------

	err, vethB.nsName = getProcessNamespace(pair_container)
	// logger(fmt.Sprintf("vethB.nsName: %v",vethB.nsName))
	if err != nil {
		err = fmt.Errorf("Failure to get docker container ns (vethb) -- %v: %v", pair_container, err)
		return err
	}

	ip, mask, err2 := net.ParseCIDR(pair_ipnetmask)
	if err2 != nil {
		err2 = fmt.Errorf("failed to parse IP addr %s: %v", pair_ipnetmask, err2)
		return err2
	}


	vethB.linkName = pair_ifname
	vethB.ipAddr.IP = ip
	vethB.ipAddr.Mask = mask.Mask
	vethB.withIPAddr = true



	// -------------------- doug debug.

	dump_vetha := spew.Sdump(vethA)
	logger("DOUG !trace vethA ----------\n" + dump_vetha)

	logger("!trace e")
	
	dump_vethb := spew.Sdump(vethB)
	logger("DOUG !trace vethB ----------\n" + dump_vethb)
  
	// ------ And finally don't forget...
	makeVeth(vethA, vethB)

	return nil

}



/**
Usage:
* case1: connect between docker container, with ip address
./koko -d centos1:link1:192.168.1.1/24 -d centos2:link2:192.168.1.2/24
* case2: connect between docker container, without ip address
./koko -d centos1:link1 -d centos2:link2 
* case3: connect between linux ns container (a.k.a. 'ip netns'), with ip address
./koko -n /var/run/netns/test1:link1:192.168.1.1/24 -n <snip>
* case4: connect between linux ns and docker container
./koko -n /var/run/netns/test1:link1:192.168.1.1/24 -d centos2:link2:192.168.1.2/24
* case5: connect docker/linux ns container to vxlan interface
./koko -d centos1:link1:192.168.1.1/24 -x eth1:1.1.1.1:10

* case6: delete docker interface
./koko -D centos1:link1
* case7: delete linux ns interface
./koko -N /var/run/netns/test1:link1
*/
func main() {

	var c int		// command line parameters.
	var err error		// if we encounter an error, it's marked here.
	const (
		ModeUnspec = iota
		ModeAddVeth
		ModeAddVxlan
		ModeDeleteLink
	)

	cnt := 0          // Count of command line parameters.
	getopt.OptErr = 0 // Any errors with peeling apart the command line options.

	// Create some empty vEth data objects.
	veth1 := vEth{}
	veth2 := vEth{}
	vxlan := vxLan{}
	mode := ModeUnspec

	// Parse options and and exit if they don't meet our criteria.
	for {
		if c = getopt.Getopt("d:n:x:hD:"); c == getopt.EOF {
			break
		}
		switch c {
		case 'd':
			if cnt == 0 {
				veth1, err = parseDOption(getopt.OptArg)
				if err != nil {
					logger(fmt.Sprintf("Parse failed %s!:%v", getopt.OptArg, err))
					usage()
					os.Exit(1)
				}
			} else if cnt == 1 {
				veth2, err = parseDOption(getopt.OptArg)
				if err != nil {
					logger(fmt.Sprintf("Parse failed %s!:%v", getopt.OptArg, err))
					usage()
					os.Exit(1)
				}
			} else {
				logger(fmt.Sprintf("Too many config!"))
				usage()
				os.Exit(1)
			}
			cnt++

		case 'D':
			if cnt == 0 {
				veth1, err = parseDOption(getopt.OptArg)
				if err != nil {
					logger(fmt.Sprintf("Parse failed %s!:%v", getopt.OptArg, err))
					usage()
					os.Exit(1)
				}
			} else {
				logger(fmt.Sprintf("Too many config!"))
				usage()
				os.Exit(1)
			}
			cnt++
			mode = ModeDeleteLink

		case 'n':
			if cnt == 0 {
				veth1, err = parseNOption(getopt.OptArg)
				if err != nil {
					logger(fmt.Sprintf("Parse failed %s!:%v", getopt.OptArg, err))
					usage()
					os.Exit(1)
				}
			} else if cnt == 1 {
				veth2, err = parseNOption(getopt.OptArg)
				if err != nil {
					logger(fmt.Sprintf("Parse failed %s!:%v", getopt.OptArg, err))
					usage()
					os.Exit(1)
				}
			} else {
				logger(fmt.Sprintf("Too many config!"))
				usage()
				os.Exit(1)
			}
			cnt++

		case 'N':
			if cnt == 0 {
				veth1, err = parseNOption(getopt.OptArg)
				if err != nil {
					logger(fmt.Sprintf("Parse failed %s!:%v", getopt.OptArg, err))
					usage()
					os.Exit(1)
				}
			} else {
				logger(fmt.Sprintf("Too many config!"))
				usage()
				os.Exit(1)
			}
			cnt++
			mode = ModeDeleteLink

		case 'x':
			vxlan, err = parseXOption(getopt.OptArg)
			mode = ModeAddVxlan

		case 'h':
			usage()
			os.Exit(0)

		}

	}

	// Assuming everything else above has worked out -- we'll continue on and make the vth pair.
	// You'll node at this point we've created vEth data objects and pass them along to the makeVeth method.
	if mode != ModeAddVxlan && cnt == 2 {
		// case 1: two container endpoint.
		fmt.Printf("Create veth...")
		makeVeth(veth1, veth2)
		fmt.Printf("done\n")
	} else if mode == ModeAddVxlan && cnt == 1 {
		// case 2: one endpoint with vxlan
		fmt.Printf("Create vxlan %s\n", veth1.linkName)
		makeVxLan(veth1, vxlan)
	} else if mode == ModeDeleteLink && cnt == 1 {
		fmt.Printf("Delete link %s\n", veth1.linkName)
		veth1.removeVethLink()
	}

}
