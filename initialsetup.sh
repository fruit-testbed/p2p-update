#Change default password
echo -e "raspberry\n[NEWPASSWORD]\n[NEWPASSWORD]" | passwd pi

#Set up ssh and reboot if not done already
if [ -e /boot/ssh ]
then
    echo "ssh already enabled"
else
    sudo touch /boot/ssh
    sudo reboot
fi

#Update apt-get
sudo apt-get update -y

#Obtain Serf zip and unzip
#CAUTION: link may change, correct as of 3-Jul-2017
sudo wget -P /usr/local/sbin https://releases.hashicorp.com/serf/0.8.1/serf_0.8.1_linux_arm.zip
sudo unzip /usr/local/sbin/serf_0.8.1_linux_arm.zip -d /usr/local/sbin/
#Cleanup zip file
sudo rm /usr/local/sbin/serf_0.8.1_linux_arm.zip
#Create serf config file
touch serf.conf
echo "Serf install complete"


host=`hostname -I`
#Strip trailing whitespace from host or serf.conf won't work
addr="$(echo "${host}" | tr -d '[:space:]')"
echo $addr
#Serf node attributes
node_name="NAME"
discover="GROUP"
role="client"
datacenter="CITY"

#Serf events and scripts
deployevent="user:deploy=/home/pi/deploy.sh"
slackevent="user:slack=/home/pi/slack.sh"
updateevent="user:update=/home/pi/update.sh"
uptimequery="query:load=uptime"

#Populate data in serf.conf
echo -e '{\n  "node_name": "'"$node_name"'",\n  "bind": "'"$addr"'",\n  "discover": "'"$discover"'",\n  "tags": {\n    "rpc-addr": "'"$addr"'",\n    "role": "'"$role"'",\n    "datacenter": "'"$datacenter"'"\n  }, \n  "event_handlers": [\n    "'"$deployevent"'",\n    "'"$slackevent"'",\n    "'"$updateevent"'",\n    "'"$uptimequery"'"\n  ]\n}' > serf.conf
echo "serf.conf populated successfully"

#Install Transmission
sudo apt-get install transmission -y
sudo apt-get install transmission-daemon -y

#Stop transmission-daemon and edit settings.json
sudo service transmission-daemon stop
sudo cat /etc/transmission-daemon/settings.json | sudo sed -i '9s/.*/    "bind-address-ipv4": "'"$addr"'",/' /etc/transmission-daemon/settings.json
echo "bind address changed"
sudo cat /etc/transmission-daemon/settings.json | sudo sed -i '25s/.*/    "lpd-enabled": true,/' /etc/transmission-daemon/settings.json
echo "lpd enabled"
sudo cat /etc/transmission-daemon/settings.json | sudo sed -i '53s/.*/    "rpc-whitelist": "127.0.0.1,192.168.\*.\*",/' /etc/transmission-daemon/settings.json
echo "rpc-whitelist changed"
sudo cat /etc/transmission-daemon/settings.json | sudo sed -i '49s/.*/    "rpc-password": "NEWPASSWORD",/' /etc/transmission-daemon/settings.json
echo "rpc-password changed"
sudo cat /etc/transmission-daemon/settings.json | sudo sed -i '52s/.*/    "rpc-username": "NEWUSERNAME",/' /etc/transmission-daemon/settings.json
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
wget https://raw.githubusercontent.com/fruit-testbed/p2p-update/master/agent.py
echo "agent.py downloaded"
sudo chmod +x agent.py
wget https://raw.githubusercontent.com/fruit-testbed/p2p-update/master/submitfile.py
echo "submitfile.py downloaded"
sudo chmod +x submitfile.py
wget https://raw.githubusercontent.com/fruit-testbed/p2p-update/master/serf-items/deploy.sh
echo "deploy.sh downloaded"
sudo chmod +x deploy.sh
wget https://raw.githubusercontent.com/fruit-testbed/p2p-update/master/serf-items/slack.sh
echo "slack.sh downloaded"
sudo chmod +x slack.sh
wget https://raw.githubusercontent.com/fruit-testbed/p2p-update/master/serf-items/update.sh
echo "update.sh"
sudo chmod +x update.sh

#Set username and password for Transmission for transmission-remote calls made by agent.py
sudo cat agent.py | sed -i '79s/USERNAME\:PASSWORD/NEWUSERNAME:NEWPASSWORD/' agent.py
sudo cat agent.py | sed -i '85s/USERNAME\:PASSWORD/NEWUSERNAME:NEWPASSWORD/' agent.py
sudo cat agent.py | sed -i '93s/USERNAME\:PASSWORD/NEWUSERNAME:NEWPASSWORD/' agent.py


#Create events log file for agent.py
touch events.log
echo "Events log created"

echo "Initial setup finished! Launching serf node..."
#Run serf in the background
serf agent -config-file serf.conf &

#Run agent.py in the background
echo "Launching agent.py..."
sudo python agent.py &

