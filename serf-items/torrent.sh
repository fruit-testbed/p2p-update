#!/bin/bash

echo "Torrent script"

while read line
do
  DATA=`cat $line`
  echo $DATA >> /tmp/sent-torrents
  echo $line >> /tmp/sent-torrents
  touch /var/lib/transmission-daemon/downloads/$line
  serf event torrentnew "$DATA"
done < /dev/stdin


