#!/bin/bash

date >> /tmp/deploy-events
echo "Deploying 'foo' parameters=$@" >> /tmp/deploy-events
echo "data:" >> /tmp/deploy-events

while read line
do
  echo "$line" >> /tmp/deploy-events
done < /dev/stdin

echo "" >> /tmp/deploy-events
