#!/bin/bash

date >> /tmp/slack-messages
echo "Slack script"
echo "Slack message sent: " >> /tmp/slack-messages

while read line
do
  echo $line >> /tmp/slack-messages
  curl -X POST -H "Content-type: application/json" --data '{"text": "'"$line"'"}' https://hooks.slack.com/[HOOK CODE HERE]
done < /dev/stdin

echo "" >> /tmp/slack-messages
