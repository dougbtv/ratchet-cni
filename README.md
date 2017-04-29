# Ratchet CNI

A [CNI](https://github.com/containernetworking/cni) plugin (generally for Kubernetes) to create multiple isolated networks using [koko](https://github.com/redhat-nfvpe/koko), the container connector. Currently, it creates a veth pair between containers to facilitate network isolation, and uses a method similar to [Multus CNI](https://github.com/Intel-Corp/multus-cni) to attach multiple interfaces to a pod, where one is 

The configuration allows you to pass-through another CNI plugin to some containers (say, Flannel), yet lets you specifically configure other pods to be eligible for treatment under the network isolation scheme as per Ratchet. For now, ones specifies labels for eligible pods to be treated under Ratchet.

More to come, it's a prototype.

## Requirements

Requires that you have [etcd](https://github.com/coreos/etcd) running, and the compute nodes in your Kubernetes cluster have network access to that etcd.

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

```
{
  "name": "ratchet-demo",
  "type": "ratchet",
  "etcd_host": "localhost",
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

## Behind the name

The name idea comes from the idea of a [ratchet puller](https://en.wikipedia.org/wiki/Come-A-Long) (aka: a come-a-long). Because in my mind it connects to something at one end, and then on the other end, you can crank on it to hold the pieces where they need to be.

## Customized these Go modules...

* `nfvpe/koko`
