# Ratchet CNI

![travis ci status](https://travis-ci.org/dougbtv/ratchet-cni.svg?branch=master)
![ratchet_logo][ratchet_logo]

A [CNI](https://github.com/containernetworking/cni) plugin (generally for Kubernetes) to create multiple isolated networks using [koko](https://github.com/redhat-nfvpe/koko), the container connector. Currently, it creates a veth pair between containers to facilitate network isolation, and uses a method similar to [Multus CNI](https://github.com/Intel-Corp/multus-cni) to attach multiple interfaces to a pod, where one is a pass-through to another CNI plugin, and the other is ratchet itself, which creates isolated veth interfaces for containers (and later, also vxlan).

The configuration allows you to pass-through another CNI plugin to some containers (say, Flannel), yet lets you specifically configure other pods to be eligible for treatment under the network isolation scheme as per Ratchet. For now, ones specifies labels for eligible pods to be treated under Ratchet.

More to come, it's a prototype.

## Requirements

Requires that you have [etcd](https://github.com/coreos/etcd) running, and the compute nodes in your Kubernetes cluster have network access to that etcd.

## Current limitations

Currently, Ratchet only uses the vEth features of Koko, and not the VXLAN functionality -- so it will only work for pods that are launched on the same minion node.

## Building Ratchet.

Requires Go 1.6. Clone this project and:

```
./build.sh
```

## Installing it

1. Place the two binaries (in the `./bin/` folder if you built it, or from the tar if you download it) has two binaries, `ratchet` and `ratchet child`, place these into the cni bin directory, typically `/opt/cni/bin/`, on each Kubernetes node.
2. Create a CNI configuration file `01-ratchet.conf` (or as you please) in the `/etc/cni/net.d/` directory.

## Sample configuration

Here's a sample configuration that uses Flannel for pods which are not eligible for treatment under Rathet, and uses a loopback device for the "boot network".

```json
{
  "name": "ratchet-demo",
  "type": "ratchet",
  "etcd_host": "localhost",
  "etcd_port": "2379",
  "child_path": "/opt/cni/bin/ratchet-child",
  "delegate": {
    "name": "cbr0",
    "type": "flannel",
    "delegate": {
      "isDefaultGateway": true
    }
  },
  "boot_network": {
    "type": "loopback"
  }
}
```

Sample configurations are also available in the `./conf/` directory.

## About configuring Ratchet

(More to come.)

### Property overview

All of these properties are required:

* `type`: required, and must be "ratchet", tells CNI what plugin to run
* `etcd_host`: the hostname or IP address of your etcd instance.
* `child_path`: path of the "child" binary.
* `delegate`: an entire CNI config nested in this property. Above sample is Flannel, this config applies to ineligible pods only.
* `boot_network`: an entire CNI config nested in this property. This is attached to each eligible pod.

**Delegate vs Boot Network**

The `delegate proper`

**How to differentiate between eligible and ineligible**

We set [labels](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/) on the pod, specifically the value `ratchet` set to true, and other configurations. See the section on "Setting up some pods".

Any pod that doesn't contain that label is ineligible for treatment under this plugin, and is passed through to the CNI plugin as defined in the `delegate` field.

## Setting up some pods

There are sample pod specs in the `./pod_specs` folder.

You can create the example pods with:

```
kubectl create -f ./pod_specs/example.yaml
```

## Looking at the pod labels.

Pairs are able to be discovered using the meta data available in the pod labels. This defines which pods will be linked together, and their defined network properties as set in the labels. These properties are then stored in etcd to allow Ratchet to connect them once the proper infra container comes up.

Looking at the `./pod_specs/example.yaml` file, let's look at what we have:

```yaml
  labels:
    app: primary-pod
    ratchet: "true"
    ratchet.pod_name: "primary-pod"
    ratchet.target_pod: "primary-pod"
    ratchet.target_container: "primary-pod"
    ratchet.public_ip: "1.1.1.1"
    ratchet.local_ip: "192.168.2.100"
    ratchet.local_ifname: "in1"
    ratchet.pair_name: "pair-pod"
    ratchet.pair_ip: "192.168.2.101"
    ratchet.pair_ifname: "in2"
    ratchet.primary: "true"
```

In this case, these labels are applied to a pod named `primary-pod` and we specify that we're going to pair with the pod which has the name in the `ratchet.pair_name` label, in this case `pair-pod`

Only one pod in the pair can have `ratchet.primary: "true"`.

The pod named `primary-pod` will be assigned `192.168.2.100` IP address on an interface named `in1`, and the pod named `pair-pod` will be assigned the IP of `192.168.2.101` on an interface named `in2` -- interfaces `in1` and `in2` are two ends of a veth pair as created by Koko. Right now these are in a statically defined `/24` network, that will be improved in the future.

...More explanation to come.

## Compiling and deploying on a remote Kubernetes

In the `./utils` directory there is an Ansible playbook to allow you to sync your current directory with a remote master, and compile ratchet there. This allows you to edit your code locally, and then deploy ratchet elsewhere. Primarily, edit the `remote.inventory` file to match your remote environment.

You can synchronize and build your source with:

```
ansible-playbook -i remote.inventory sync-and-build.yml
```



## Behind the name

The name idea comes from the idea of a [ratchet puller](https://en.wikipedia.org/wiki/Come-A-Long) (aka: a come-a-long). Because in my mind it connects to something at one end, and then on the other end, you can crank on it to hold the pieces where they need to be.

## Customized these Go modules...

* `nfvpe/koko`


[ratchet_logo]: docs/ratchet.png
