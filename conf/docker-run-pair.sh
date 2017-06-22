#!/bin/bash

# Run a docker container with network namespace set up by the
# CNI plugins.

# Example usage: ./docker-run.sh --rm busybox /sbin/ifconfig

contid=$(docker run --name pair_infra -d --net=none --label-file ./pair.label busybox:latest /bin/sleep 10000000)
pid=$(docker inspect -f '{{ .State.Pid }}' $contid)
netnspath=/proc/$pid/ns/net

./exec-plugins.sh add $contid $netnspath

# Not cleaning up for now... Doug.
# function cleanup() {
#   ./exec-plugins.sh del $contid $netnspath
#   docker kill $contid >/dev/null
# }
# trap cleanup EXIT

docker run --net=container:$contid $@

