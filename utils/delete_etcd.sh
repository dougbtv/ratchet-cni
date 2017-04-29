#!/bin/bash
# curl -s -L -X DELETE http://localhost:2379/v2/keys/ratchet/byname/pair_container
curl -s -L -X DELETE http://localhost:2379/v2/keys/ratchet\?recursive=true


