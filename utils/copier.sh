#!/bin/bash
# scp -P 2222 -i ~/.ssh/id_testvms nugator centos@localhost:/home/centos/cni/bin/
echo "Copying files..."
rsync -az -e 'ssh -p 2222 -i ~/.ssh/id_testvms' --exclude '/nugator' ./ centos@localhost:/home/centos/gocode/src/nugator
echo "Building source..."
ssh -p 2222 -i ~/.ssh/id_testvms centos@localhost "cd /home/centos/gocode/src/nugator; export GOPATH=/home/centos/gocode/; go build; cp -f nugator /home/centos/cni/bin"

