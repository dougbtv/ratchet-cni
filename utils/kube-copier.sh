#!/bin/bash
JUMP_HOST=192.168.1.119
KUBE_SERVERS=(192.168.122.100 192.168.122.133)

echo "Copying files..."
rsync -az -e 'ssh -p 2222 -i ~/.ssh/id_testvms' ./ centos@localhost:/home/centos/gocode/src/ratchet

# SCP with a jump host.
# scp -i ~/.ssh/id_testvms -oProxyJump=root@192.168.1.119 ../bin/ratchet centos@192.168.122.100:~

for server in ${KUBE_SERVERS[@]}; do
  echo $server
done