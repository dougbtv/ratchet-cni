#!/bin/bash

# Exit when proper environments aren't available
if [ -z ${DEFAULT_GW+x} ]; then echo "DEFAULT_GW env var is unset, exiting"; exit 1; fi
if [ -z ${DEFAULT_DEV+x} ]; then echo "DEFAULT_DEV env var is unset, exiting"; exit 1; fi
if [ -z ${WAN_IP+x} ]; then echo "WAN_IP env var is unset, exiting"; exit 1; fi

# wait until you see target device
tries=0
until ip a | grep $DEFAULT_DEV
do
  sleep 1
  let "tries+=1"
  if [ $tries -ge 30 ]; then echo "couldn't find $DEFAULT_DEV"; exit 1; fi
done

echo "Setting default route to: $DEFAULT_GW via $DEFAULT_DEV"
ip route add default via $DEFAULT_GW dev $DEFAULT_DEV

echo "Setting WAN IP address to: $WAN_IP"
ip a add $WAN_IP/255.255.255.255 dev lo;

# Sleep forever.
while :; do sleep 10; done
