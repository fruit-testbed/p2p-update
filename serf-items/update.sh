#!/bin/bash

#date >> /tmp/sent-torrents
echo "Torrent script"
#echo "Torrent file sent: " >> /tmp/sent-torrents

while read line
do
  #$@ to print all arguments (ie. the message to be sent)
#  DATA=`cat $line`
#  echo $DATA >> /tmp/sent-torrents
  echo $line >> ~/receivedtorrent.torrent
#  touch /var/lib/transmission-daemon/downloads/$line
#  serf event torrentnew "$DATA"
  echo $line >> /tmp/sent-torrents
done < /dev/stdin

echo "" >> /tmp/sent-torrents

