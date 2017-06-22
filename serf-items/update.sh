#!/bin/bash

echo "Torrent file transfer script"
echo date >> /tmp/sent-torrents

while read line
do
  echo $line > ~/receivedtorrent.torrent
  echo $line >> /tmp/sent-torrents
  date >> ~/events.log
  echo "update" >> ~/events.log
done < /dev/stdin

echo "" >> /tmp/sent-torrents

