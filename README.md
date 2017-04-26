# nugator

nugator - a pipsqueak of a CNI plugin

(it's Latin for "pipsqueak")

# How to make a CNI plugin

first we start by using [something familiar, like Multus](https://raw.githubusercontent.com/Intel-Corp/multus-cni/master/multus/multus.go)

Then we find the [running the plugins section of the CNI readme](https://github.com/containernetworking/cni#running-the-plugins), which notes that you can run the included one there -- including a "just docker" sample.

And there's the [CNI specification proper](https://github.com/containernetworking/cni/blob/master/SPEC.md)

## Setting up your environment.

I'm starting with a CentOS 7.3 box (in my case, Generic Cloud), and then I installed [Docker per the official instructions](https://docs.docker.com/engine/installation/linux/centos/).

Clone the CNI repo.

    [centos@cni ~]$ git clone https://github.com/containernetworking/cni.git

And we'll need go, Version 1.5+.

    [centos@cni ~]$ sudo yum install -y golang
    [centos@cni ~]$ go version
    go version go1.6.3 linux/amd64

Also, it'll be convenient for the centos user to access docker, so let's allow that, too.

```
[centos@cni ~]$ sudo gpasswd -a ${USER} docker
Adding user centos to group docker
[centos@cni ~]$ sudo systemctl restart docker
```

And I also need to logout and log back in and then `docker ps` to check it works. Or maybe `newgrp docker` will work for you.

And, you're gonna need jq.

```
[centos@cni ~]$ sudo curl -Ls -o /usr/bin/jq -w %{url_effective} https://github.com/stedolan/jq/releases/download/jq-1.5/jq-linux64
[centos@cni ~]$ sudo chmod +x /usr/bin/jq
[centos@cni ~]$ /usr/bin/jq  --version
jq-1.5
```

## Let's run one of the included plugins.

So `cd` into the clone, and let's create a netconf file as specified in the tutorial.

Make the appropriate default directories

    [centos@cni cni]$ sudo mkdir -p /etc/cni/net.d
    [centos@cni cni]$ sudo chown centos:centos /etc/cni/net.d

And now create the `10-mynet.conf`:

```json
[centos@cni cni]$ cat >/etc/cni/net.d/10-mynet.conf <<EOF
{
  "cniVersion": "0.2.0",
  "name": "mynet",
  "type": "bridge",
  "bridge": "cni0",
  "isGateway": true,
  "ipMasq": true,
  "ipam": {
    "type": "host-local",
    "subnet": "10.22.0.0/16",
    "routes": [
      { "dst": "0.0.0.0/0" }
    ]
  }
}
EOF
```

And we need a "loopback config".

```json
[centos@cni cni]$ cat >/etc/cni/net.d/99-loopback.conf <<EOF
{
  "cniVersion": "0.2.0",
  "type": "loopback"
}
EOF
```

And we build the plugins... (It's fairly quick)

```
[centos@cni cni]$ ./build.sh 
```

Now we can run a container with network namespace setup by CNI plugins... And we'll have that container run `ifconfig`

```
[centos@cni cni]$ CNI_PATH=`pwd`/bin
[centos@cni cni]$ cd scripts
[centos@cni scripts]$ sudo CNI_PATH=$CNI_PATH ./docker-run.sh --rm busybox:latest ifconfig | grep -P 'eth0|inet addr.+10'
eth0      Link encap:Ethernet  HWaddr 0A:58:0A:16:00:04  
          inet addr:10.22.0.4  Bcast:0.0.0.0  Mask:255.255.0.0

```

And you'll notice that container has an IP address assigned in the `10.22.0.0/16` range as specified by our `/etc/cni/net.d/10-mynet.conf` -- which you can change and spin up another container, and you'll see it has taken effect for each container you spin up.

I added [govendor](https://github.com/kardianos/govendor) to use for vendoring, which is handy.

## Let's try to setup a simple scenario to use some go code....

I'm going to make a fork of Multus and try to get it to run, and output some debug info so I can see WTH is going on. If that works -- I'm going to try to integrate it with koko.

here goes nothing.

Setup go src basics.

```
[centos@cni bin]$ mkdir -p ~/gocode/src/nugator
```

And I copy up my code by setting a tunnel...

```
ssh -L 2222:192.168.122.7:22 root@192.168.1.119
```

And then I scp things to it...

```
scp -P 2222 -i ~/.ssh/id_testvms docs/nugator.go centos@localhost:~/gocode/src/nugator
```

And let's try to build it...

```
[centos@cni bin]$ export GOPATH=/home/centos/gocode/
```

Looking at what it does, first thing I ran into it is that it uses [skel](https://godoc.org/github.com/containernetworking/cni/pkg/skel) which took me yak shaving to get vendoring going. Now, let's try to do things with it.

## Ok, so you wanna try something with it, as it stands before modifications

scp the bin (boy that takes... too long...)

```
scp -P 2222 -i ~/.ssh/id_testvms nugator centos@localhost:/home/centos/cni/bin/
```

Let's change the `/etc/cni/net.d/10-mynet.conf`, with these contents.

```json
{
  "name": "nugator-demo",
  "type": "nugator",
  "delegates": [
    {
      "type": "macvlan",
      "master": "eth0",
      "mode": "bridge",
      "ipam": {
        "type": "host-local",
        "subnet": "192.168.122.0/24",
        "rangeStart": "192.168.122.200",
        "rangeEnd": "192.168.122.216",
        "routes": [
          { "dst": "0.0.0.0/0" }
        ],
        "gateway": "192.168.122.1"
     }
    },
    {
      "cniVersion": "0.2.0",
      "name": "mynet",
      "type": "bridge",
      "bridge": "cni0",
      "isGateway": true,
      "ipMasq": true,
      "ipam": {
        "type": "host-local",
        "subnet": "10.22.0.0/16",
        "routes": [
          { "dst": "0.0.0.0/0" }
        ]
      }
    }
  ]
}
```

And then you can spin up a container, and see that it performs as multus...

```
[centos@cni scripts]$ sudo CNI_PATH=$CNI_PATH ./docker-run.sh --rm busybox:latest ifconfig | grep -P "(^\w|inet addr)"
lo        Link encap:Local Loopback  
          inet addr:127.0.0.1  Mask:255.0.0.0
net0      Link encap:Ethernet  HWaddr 0A:58:C0:A8:7A:CB  
          inet addr:192.168.122.203  Bcast:0.0.0.0  Mask:255.255.255.0
net1      Link encap:Ethernet  HWaddr 0A:58:0A:16:00:05  
          inet addr:10.22.0.5  Bcast:0.0.0.0  Mask:255.255.0.0
```

And then 

## Some debug output?

If you're going to `spew`, [spew into this little cup man](https://www.youtube.com/watch?v=ouDDj6kr1qo). I used `spew` to dump some datastructures, reminds me of Perl's `data::dumper` in its own right. I added `github.com/davecgh/go-spew/spew` to my imports and...

And added to the `loadNetConf` method, this following section to take a look at what was parsed...

```
  str := spew.Sdump(netconf)
  os.Stderr.WriteString("DOUG !trace ----------\n" + str)
```

## Handy copying util

This copies from my workstation to my test environment, and builds the script out there (which is faster than copying a fat binary in my case)

```
./utils/copier.sh
```

## Ok, sidequest: The announcer.

We're going to have pods announce that they're up with a companion job that picks up the container ID and shoots some data into etcd. Let's get that going cause we're going to need to generally emulate this in development so that the CNI plugin can read up that info.

* Create [etcd pods according to my gist for k8s jobs](https://gist.github.com/dougbtv/67589a7b3e443d1b4e2cdf05698f58ca)

Then spin up the `./announcer/sample.yaml` with:

```
[centos@kube-master announcer]$ kubectl create -f sample.yaml 
[centos@kube-master announcer]$ watch -n1 kubectl get pods --show-all
[centos@kube-master announcer]$ kubectl logs -f sample-announce sample-announce
```

And you can see it have the steps I outlined on my pad...

* Get container ID
* store container id in etcd, along with meta data.

Generally with this kind of environment:

```yaml
env:
  - name: "POD_NAME"
    value: "sample-announce"
  - name: "TARGET_CONTAINER"
    value: "target-container"
  - name: "PUBLIC_IP"
    value: "1.1.1.1"
  - name: "LOCAL_IP"
    value: "192.168.2.100"
  - name: "LOCAL_IFNAME"
    value: "in1"
  - name: "PAIR_NAME"
    value: "pair-pod"
  - name: "PAIR_IP"
    value: "192.168.2.101"
  - name: "PAIR_IFNAME"
    value: "in2"
  - name: "PRIMARY"
    value: "true"
```

## Big questions still left

Primarily: If we're controlling how the pod gets its network -- how does it get enough network to talk to Kubernetes API? 

Yikes. So...

current game plan: Use multus CNI. Have it create flannel networking for the pod, and then secondarily -- it calls nugator (new name? ratchet.)

## Calling another module in go...

Is koko all set to go?

let's try it.

have to modify koko.

change "package main" to "package koko".

Public methods start with an upper case letter.

And... 

We need to abstract the main() which is.... all for command line.



## Etcd. 

Installed etcd (v2? e.g. not v3) from yum, enabled, started. Test it...

And it's there.

```
[centos@cni scripts]$ curl -L -X PUT http://localhost:2379/v2/keys/message -d value="sup sup"
{"action":"set","node":{"key":"/message","value":"sup sup","modifiedIndex":4,"createdIndex":4}}
[centos@cni scripts]$ curl -L  http://localhost:2379/v2/keys/message 
{"action":"get","node":{"key":"/message","value":"sup sup","modifiedIndex":4,"createdIndex":4}}
```

## Getting etcd data in go

## Check if partner is up

## ASk if primary

## Execute koko