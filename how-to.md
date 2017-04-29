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

## Necessary steps...

* Getting etcd data in go
* Check if partner is up
* ASk if primary
* Execute koko

## Getting ready to run in kubernetes

Where will my logs be?

```
dougbtv [8:49 AM] 
I'm working on a sample/toy CNI plugin, and I'm making some solid progress developing it on a local machine using the `./scripts/docker-run.sh` script from the CNI github repo -- I output to stdout when I need to debug -- can I use the same methodology when I run it with kubernetes, and where will those logs wind up? (edited)

[8:50] 
(or, should I write to a flat logfile, or... I'll take any recommendations, thanks)

aanm [8:52 AM] 
@dougbtv the cni output should appear in kubelet logs

dougbtv [8:52 AM] 
@aanm awesome! appreciate the pointer

[8:53] 
(also, thank you! cheers :coffee: )

aanm [8:54 AM] 
@dougbtv I'm not sure if you have to run kubelet with a more verbose log message, I've running with `--v=2` and I see the logs
```

More discussion there -- I guess it's sort of unknown. How the heck do these guys actually trial their stuff? *shrugs*

## Running through the tests

Ok, so in the `./utils/` folder there three scripts.

You can use them to populate etcd data as necessary.

```
[centos@cni scripts]$ ./delete_etcd.sh 
[centos@cni scripts]$ ./etcd_populator.sh 774cff155aca9e101b222a6687ba332fc66fe3194d92c07efed30e20527f4666
[centos@cni scripts]$ ./pair_etcd_populator.sh ea74dcb8e17688297bb6e74d7a69e110b06dafeafcb799430dc5fd8267764175
```

E.g. spin up a `docker-run.sh` instance, grab the containerid, then use 'em there.

## Some fixable mini snags

Yeah, so right now it's not deleting the interfaces, so...

```
[centos@cni scripts]$ sudo ip link delete in1
```

## So let's run this mutha on kube.

Spinning up my playbooks with a single minion, and using multus. Working on a `customcni` branch.

Let's baseline it and make sure that it's working default, first.

Kick ass, baseline multus appears to work.

## Idea -- what are the multiple configurations?

Does it just do the first one? Or... did multus fail? I had an accident where I used just flannel plain.

Let's try that in the test bed docker only spot, with two plugins. 

Cause I don't give a shizzat if there's "real interfaces created by CNI" right now -- I'm guessing that's for real, I just wanna run my script.

## bashgator test

```
[root@kubecni-minion-1 cni]# locate bashgator
/etc/cni/net.d/11-bashgator.conf
/opt/cni/bin/bashgator
[root@kubecni-minion-1 cni]# cd /etc/cni/net.d/
[root@kubecni-minion-1 net.d]# 
[root@kubecni-minion-1 net.d]# 
[root@kubecni-minion-1 net.d]# pwd
/etc/cni/net.d
[root@kubecni-minion-1 net.d]# ls
10-multus.conf  11-bashgator.conf
[root@kubecni-minion-1 net.d]# cat *
{
  "name": "multus-demo",
  "type": "multus",
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
      "type": "flannel",
      "masterplugin": true,
      "delegate": {
        "isDefaultGateway": true
      }
    }
  ]
}
{
  "name": "bashgator-demo",
  "type": "bashgator",
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
      "type": "flannel",
      "masterplugin": true,
      "delegate": {
        "isDefaultGateway": true
      }
    }
  ]
}
[root@kubecni-minion-1 net.d]# cat /opt/cni/bin/bashgato
r#!/bin/bash
echo "$(date) check 1 2, 1 2" >> /tmp/bashgator.sh
>&2 echo "This is a test, here is bashgator"
```

So.... "kind of worked".

That is, I can get it to execute, but... if it fails -- it doesn't execute the next plugin. And... guess what? multus is failing, so bashgator has to be first -- and multus doesn't execute. Or multus is first and bashgator doesn't execute.

So, that's a catch 22.

Let's try with flannel plain config. I'm gonna rebuild the boxen.

```
Apr 28 00:50:53 kubecni-minion-1 kubelet[11812]: This is a test, here is bashgator
Apr 28 00:50:53 kubecni-minion-1 kubelet[11812]: E0428 00:50:53.030371   11812 cni.go:257] Error adding network: unexpected end of JSON input
Apr 28 00:50:53 kubecni-minion-1 kubelet[11812]: E0428 00:50:53.030405   11812 cni.go:211] Error while adding to cni network: unexpected end of JSON input
Apr 28 00:50:53 kubecni-minion-1 kubelet[11812]: W0428 00:50:53.047766   11812 docker_sandbox.go:263] NetworkPlugin cni failed on the status hook for pod "nginx-k7b0k_default": Unexpected command output nsenter: cannot open /proc/2213/ns/net: No such file or directory
```

Ok, so... it doesn't really like my script. I had to put it first too.

Next thing to do? I guess we gotta try to make sure skel is in action cause, I'll betchya that does some friendly things.

Let's try a simple version of nugator.

Fails in the same way with the early exit.

Next steps.... ? I think it's time to try one of two things:

* Try to come to completion of the script without an early exit? Unsure what it will change.
* Try to complete the script in the same way that multus does, delegate to a loopback?
* Try to run it with multus cni.

## Check this out in multus

This is interesting at least -- it shows that it [outputs some result, apparently](https://github.com/Intel-Corp/multus-cni/blob/master/multus/multus.go#L179-L188).

Apparently in the `delegateAdd` method...

```golang
  result, err := invoke.DelegateAdd(netconf["type"].(string), netconfBytes)
  if err != nil {
    return true, fmt.Errorf("Multus: error in invoke Delegate add - %q: %v", netconf["type"].(string), err)
  }

  if !isMasterplugin(netconf) {
    return true, nil
  }

  return false, result.Print()
```

Take a little bit of a better look, we can see that the `invoked.DelegateAdd` returns us a type of [Result, which has a Print() method to print json to stdout, GoDoc here](https://godoc.org/github.com/containernetworking/cni/pkg/types#Result)

## Ineligible containers!

Sometimes, the containers shouldsn't be eligible for this treatment, we just delegate off.

Use a `com.ratchet` label set to any value.

```
[centos@cni scripts]$ cat /tmp/center/Dockerfile 
FROM centos:centos7
LABEL "com.ratchet"="true"
RUN yum install -y traceroute iproute net-tools tcpdump nano vi
```

And it's in the docker inspect.

```
[centos@cni scripts]$ docker inspect dreamy_bohr | grep -i ratch
                "com.ratchet": "true",
```

Now, how the hell to pull that out? Got it.

## LABEL TIME.

Ok, we're going to do a lot more with labels, it's how we're going to learn all about ourself, and who our pair is.

Then we'll pair up async. This is due to a big fat chicken-and-egg with infra containers. And infra containers are unknown to the API, too.

Now... How to test this...

First I modify the `docker-run.sh` from CNI proper which emulates the use of infra containers. Then I use the `--label-file` parameter for `docker-run` this way we can play in dev. Looks like this.

```
[centos@cni scripts]$ diff docker-run.sh docker-run-custom.sh 
8c8
< contid=$(docker run -d --net=none busybox:latest /bin/sleep 10000000)
---
> contid=$(docker run -d --net=none --label-file ./primary.label busybox:latest /bin/sleep 10000000)

[centos@cni scripts]$ cat primary.label 
# Comment like so.
ratchet=true
ratchet.pod_name=primary-pod
ratchet.target_pod=primary-pod
ratchet.target_container=primary-pod
ratchet.public_ip=1.1.1.1
ratchet.local_ip=192.168.2.100
ratchet.local_ifname=in1
ratchet.pair_name=pair-pod
ratchet.pair_ip=192.168.2.101
ratchet.pair_ifname=in2
ratchet.primary=true


[centos@cni scripts]$ sudo CNI_PATH=$CNI_PATH ./docker-run-custom.sh --label-file ./primary.label  --rm center sleep 700
```

## Child process refactor...

starting to question it.

Let's experiment with a revert.

