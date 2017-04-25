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
[centos@cni scripts]$ sudo CNI_PATH=$CNI_PATH ./docker-run.sh --rm busybox:latest ifconfig | grep -P 'eth0|inet addr.+10'
eth0      Link encap:Ethernet  HWaddr 0A:58:0A:16:00:04  
          inet addr:10.22.0.4  Bcast:0.0.0.0  Mask:255.255.0.0

```

And you'll notice that container has an IP address assigned in the `10.22.0.0/16` range as specified by our `/etc/cni/net.d/10-mynet.conf` -- which you can change and spin up another container, and you'll see it has taken effect for each container you spin up.

## Let's try to setup a test scenario.

I'm going to make a fork of Multus-CNI and try to get it to run, and output some debug info so I can see WTF is going on. If that works -- I'm going to try to integrate it with koko.

here goes nothing.

Setup go src basics.

```
[centos@cni bin]$ mkdir -p ~/gocode/src/nugator
```

And I copy up my code by setting a tunnel...

```
ssh -L 2222:192.168.122.7:22 root@192.168.1.119
```

And then I scp it...

```
scp -P 2222 -i ~/.ssh/id_testvms docs/nugator.go centos@localhost:~/gocode/src/nugator
```

And let's try to build it...

```
[centos@cni bin]$ export GOPATH=/home/centos/gocode/
```

