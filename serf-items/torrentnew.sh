#!/bin/bash

echo "Torrent script"

while read line
do
  FILENAME=`tail -n 1 /tmp/sent-torrents`
  #Put data received as payload into file
  echo $line > /var/lib/transmission-daemon/downloads/$FILENAME
  
done < /dev/stdin
