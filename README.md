# p2p-update
Peer to Peer Update project

**Section 1:** Update system for LAN

**Section 2:** Update system with NAT traversal

# Section 1: Update system for LAN

## Setup guides

**initialsetup.py** automates the setup of the components required for the peer-to-peer update system, subject to users supplying authorisation details for the local machine, `transmission-remote` procedure calls, and local server for setting correct system time. This will be made simpler in the near future by allowing a config file as a command line argument for the script.

In-depth guides are available for each component:
* [Transmission](https://github.com/fruit-testbed/p2p-update/blob/master/transmission-items/setup.md "Transmission setup guide")
* [Serf](https://github.com/fruit-testbed/p2p-update/blob/master/serf-items/setup.md "Serf setup guide")
* [Puppet](https://github.com/fruit-testbed/p2p-update/blob/master/puppet-items/setup.md "Puppet setup guide")


## Submitting a file

**submitfile.py** allows a user to submit a file to be downloaded by other nodes in the swarm.

To submit a file to be transmitted to the rest of the swarm, use the following command:
`$ sudo python submitfile.py [FILE]`

This script copies the file to the transmission downloads directory, obtains the torrent file metadata and the MD5 hash of the torrent file, and encodes the `pieces` section of the torrent metadata using base64 to prevent corruption of binary data in transit. It then combines the MD5 hash and partially-encoded torrent metadata into one string, sending it as a payload for the Serf 'update' event.

**update.sh** writes the received MD5 hash and torrent file data to **~/receivedtorrent.torrent**. It also writes a timestamp and event descriptor (`update`) to **events.log**, which will trigger torrent management and version checking in **agent.py**.


## Sending torrent files over Serf

Users should, in most cases, avoid sending torrent data directly using Serf and should use **submitfile.py** script described below instead - this script handles torrent creation, formatting and data broadcasting for all nodes.

If **.torrent** file data must be sent directly without the aid of the file submission script, use the following command:

``$ serf event update "$`cat [FILE].torrent`"``.

The payload given by ``"$`cat [FILE].torrent`"`` will be stored in **~/receivedtorrent.torrent**. Note that data transmitted through this command is not likely to work with **agent.py** due to the lack of a sent MD5 hash and base64 encoding of the torrent file binary data.

Serf user events have a 512 byte limit. Torrent files of just over 200 bytes can be reliably transmitted - further work is being done to establish the exact limit on this, and hopefully to increase this limit.


## Automated management script

**agent.py** is an active listening management script to automate the process of receiving Serf events, adding torrents, and applying new update files for each node within the swarm. To run this script, use the following command:
`$ sudo python agent.py`

Currently **agent.py** does the following:
* Writes received MD5 hash and torrent file metadata to ~/receivedtorrent.torrent
* Separates MD5 hash and raw torrent metadata
* Decodes base64-encoded sections of the torrent metadata using **torrentformat.py**
* Creates a torrent file based on sent torrent creation date to the correct transmission directory
* Checks other torrent files in this directory to see if this is the newest torrent file available
* Checks the MD5 hash received from Serf against the MD5 hash of the locally reconstructed torrent file
* Adds new torrent to transmission-daemon if and only if the torrent file is the newest available and has a matching MD5 hash
* Monitors torrents for completed downloads
* Applies update manifests and installs modules as soon as they have finished downloading

**_NOTE_**: lines 119, 125 and 135 contain calls to `os.system` which require the user to enter the username and password information for their transmission client(s). Future work on this project includes allowing **agent.py** to use a config file for these values.


## Torrent formatting

**torrentformat.py** has four functions:
* `encodetorrent(torrent)`: encodes the `pieces` section of torrent file metadata using base64 to prevent corruption of binary data in transit (null chars being removed when sent over Serf, backslashes occasionally interpreted as escape chars, etc.)
* `decodetorrent(torrent)`: decodes the `pieces` section back into binary data so the reconstructed torrent file is valid
* `appendmd5(string, torrentfile)`: takes the MD5 hash of torrentfile, then appends it to the start of the string containing the data to be sent over Serf as a payload for an update event
* `removemd5(string)`: separates the received Serf payload data into two sections: MD5 hash and torrent metadata
    
This module is used in both **submitfile.py** and **agent.py**.


# Section 2: Update system with NAT traversal


**NAT-traversal** contains five files:

* **stunclientlite.py**: opens a UDP link to a proxy server, which can then be used to send or receive session invites from other peers. Usage:

`$ python stunclientlite.py (proxy-server-address) (proxy-server-port)`
* **stunserverlite.py**: acts as a proxy server - listens for messages from clients and distributes address and port information to enable peer-to-peer sessions. Usage:

`$ python stunserverlite.py (address) (port)`
* **eventcreate.py**: sends an event and payload to the client script via a UDP socket bound to `localhost` - these can be sent to other peers or the proxy server through the client script. These are the commands currently understood by this script:

   * `$ python eventcreate.py talkto (IP-addr-used-by-peer)`: contacts proxy server to start the process of establishing a peer-to-peer session with the machine at the given address/behind a NAT with the given address.

   * `$ python eventcreate.py sendfile (path-to-file)`: creates a torrent file from the file or directory given, then appends the MD5 hash of the torrent file to the torrent metadata as part of the event payload. This payload is then broadcast  Uses a modified version of **torrentformat.py**.

   * `$ python eventcreate.py endsession`: alerts peers in the current peer-to-peer session that this machine is leaving, leaves the session, then resumes contact with proxy server.
   
   * `$ python eventcreate.py exit`: as above, but also alerts proxy server of shutdown so it can remove machine's details from its dictionary of potential peers, then exits the program.
   
* **agent.py**: modified version of agent script from section 1. Monitors system for received torrent files and checks received MD5 hash in payload against the calculated value of the locally reconstructed torrent file to check that it has not been tampered with or corrupted in transit. Torrent is added to `transmission-daemon` if this check is passed. Usage:

`$ python agent.py`

Any machine can initiate a peer-to-peer session regardless of the type of NAT obscuring the peer being contacted. This is done by getting each machine to retransmit `TalkTo` messages to mark the `addr:port` combination as 'familiar' to Restricted NAT - the NAT will then allow future traffic from `addr:port`.

