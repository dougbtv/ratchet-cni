#!/bin/bash
# scp -P 2222 -i ~/.ssh/id_testvms nugator centos@localhost:/home/centos/cni/bin/
echo "Copying files..."
rsync -az -e 'ssh -p 2222 -i ~/.ssh/id_testvms' ./ centos@localhost:/home/centos/gocode/src/ratchet
echo "Building source..."
ssh -p 2222 -i ~/.ssh/id_testvms centos@localhost "cd /home/centos/gocode/src/ratchet/ratchet; export GOPATH=/home/centos/gocode/; go build; cp -f ratchet /home/centos/cni/bin"
echo "Building child source..."
ssh -p 2222 -i ~/.ssh/id_testvms centos@localhost "cd /home/centos/gocode/src/ratchet/ratchet-child; export GOPATH=/home/centos/gocode/; go build; cp -f ratchet-child /home/centos/cni/bin"