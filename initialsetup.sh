#!/bin/bash

#####
#
# This script sets up the required the software components of P2P-Update.
#
#####

#Serf node attributes
node_name=$(hostname)
discover="fruit"
role="client"
datacenter="cloud"
transmissionuser="fruit"
transmissionpassword="fruit"

mkdir -p p2p-update
cd p2p-update
home=$(pwd)

addr="$(hostname -I | tr -d '[:space:]')"
echo "ip-address: $addr"

# Install Serf
apt search serf | grep 'serf\/stable'
if [ $? -eq 0 ]; then
  sudo apt-get install serf -y
elif [ ! -f ./serf ] && [ "$(which serf)" = "" ]; then
  echo "WARNING: Cannot find serf in apt repositories. You must install it manually."
fi

#Serf events and scripts
deployevent="user:deploy=$home/deploy.sh"
updateevent="user:update=$home/update.sh"
uptimequery="query:load=uptime"

#Populate data in serf.conf
cat > serf.conf <<EOL
{
  "node_name": "$node_name",
  "bind": "$addr",
  "discover": "$discover",
  "tags": {
    "rpc-addr": "$addr",
    "role": "$client",
    "datacenter": "$datacenter"
  }, 
  "event_handlers": [
    "$deployevent",
    "$updateevent",
    "$uptimequery"
  ]
}
EOL
echo "serf.conf populated successfully"

# Ensure NTPD is installed and running
sudo apt-get install ntp -y && sudo systemctl start ntpd

# Transmission
sudo apt-get install transmission transmission-daemon unzip -y

#Stop transmission-daemon and edit settings.json
sudo service transmission-daemon stop
echo "Updating transmission-daemon settings..."
sudo cat /etc/transmission-daemon/settings.json | sudo sed -i '9s/.*/    "bind-address-ipv4": "'"$addr"'",/' /etc/transmission-daemon/settings.json
echo "bind address changed"
sudo cat /etc/transmission-daemon/settings.json | sudo sed -i '25s/.*/    "lpd-enabled": true,/' /etc/transmission-daemon/settings.json
echo "lpd enabled"
sudo cat /etc/transmission-daemon/settings.json | sudo sed -i '53s/.*/    "rpc-whitelist": "127.0.0.1,192.168.\*.\*,10.*",/' /etc/transmission-daemon/settings.json
echo "rpc-whitelist changed"
sudo cat /etc/transmission-daemon/settings.json | sudo sed -i "49s/.*/    \"rpc-password\": \"$transmissionpassword\",/" /etc/transmission-daemon/settings.json
echo "rpc-password changed"
sudo cat /etc/transmission-daemon/settings.json | sudo sed -i "52s/.*/    \"rpc-username\": \"$transmissionuser\",/" /etc/transmission-daemon/settings.json
echo "rpc-username changed"
sudo cat /etc/transmission-daemon/settings.json | sudo sed -i '66s/.*/    "umask": 2,/' /etc/transmission-daemon/settings.json
echo "umask changed"
#Start transmission-daemon
sudo service transmission-daemon start
echo "Transmission setup complete"


#Install puppet
sudo apt-get install puppet -y
echo "Puppet install complete"

#Obtain agent.py, submitfile.py and Serf scripts
wget -q https://raw.githubusercontent.com/fruit-testbed/p2p-update/intern17/agent.py
wget -q https://raw.githubusercontent.com/fruit-testbed/p2p-update/intern17/torrentformat.py
echo "agent.py downloaded"
chmod +x agent.py
wget -q https://raw.githubusercontent.com/fruit-testbed/p2p-update/intern17/submitfile.py
echo "submitfile.py downloaded"
chmod +x submitfile.py
wget -q https://raw.githubusercontent.com/fruit-testbed/p2p-update/intern17/serf-items/deploy.sh
echo "deploy.sh downloaded"
chmod +x deploy.sh
wget -q https://raw.githubusercontent.com/fruit-testbed/p2p-update/intern17/serf-items/update.sh
echo "update.sh"
chmod +x update.sh

#Set username and password for Transmission for transmission-remote calls made by agent.py
sed -i "s/\[USERNAME\]\:\[PASSWORD\]/$transmissionuser:$transmissionpassword/" agent.py
sed -i "s/\[USERNAME\]\:\[PASSWORD\]/$transmissionuser:$transmissionpassword/" agent.py
sed -i "s/\[USERNAME\]\:\[PASSWORD\]/$transmissionuser:$transmissionpassword/" agent.py


#Create events log file for agent.py
sudo touch /var/log/events.log
echo "Events log created"

echo "Initial setup finished! Launching serf node..."
#Run serf in the background
if [ "$1" = "" ]; then
  nohup serf agent -config-file serf.conf &
  echo "start serf agent as standalone"
else
  nohup serf agent -config-file serf.conf -join "$1" &
  echo "start serf agent joining $1"
fi

#Run agent.py in the background
echo "Launching agent.py..."
sudo nohup python agent.py &
