#!/bin/bash

echo "Torrent file transfer script"
echo date >> /tmp/sent-torrents

while read line
do
  echo $line > ~/receivedtorrent.torrent
  echo $line >> /tmp/sent-torrents
done < /dev/stdin

echo "" >> /tmp/sent-torrents

