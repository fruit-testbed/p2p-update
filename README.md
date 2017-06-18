# p2p-update
Peer to Peer Update project

## Setup guides
* [Transmission](https://github.com/fruit-testbed/p2p-update/blob/master/transmission-items/setup.md "Transmission setup guide")
* [Serf](https://github.com/fruit-testbed/p2p-update/blob/master/serf-items/setup.md "Serf setup guide")
* [Puppet](https://github.com/fruit-testbed/p2p-update/blob/master/puppet-items/setup.md "Puppet setup guide")

## Sending torrent files over Serf

**.torrent** files can be sent as a user event payload to other nodes using Serf. This is done using the following command:

``$ serf event update "$`cat [FILE].torrent`"``.

Users should, in most cases, avoid using this and use **submitfile.py** script described below - this handles torrent creation and download management for all nodes.

These payloads are currently stored in **~/received-torrent.torrent**.

Note that Serf user events have a 512 byte limit. Torrent files of just over 200 bytes can be reliably transmitted - further work is being done to establish the exact limit on this, and to hopefully increase this limit.

## Submitting a file

**submitfile.py** allows a user to submit a file to be downloaded by other nodes in the swarm. This script copies the file to the transmission downloads directory, creates a torrent file and sends the data as a payload for the Serf 'update' event.

**update.sh** writes a timestamp and event descriptor (`update`) to **events.log**, which will trigger torrent management and version checking in **agent.py**.

## Automated management script

**agent.py** is an active listening management script to automate the process of torrenting and applying new update files for each node within the swarm. To run this script, use the following command in a separate terminal instance:
`$sudo python agent.py`

Currently **agent.py** does the following:

    * Writes received torrent file data to ~/receivedtorrent.torrent
    * Creates a torrent file based on sent torrent creation date to the correct transmission directory
    * Checks other torrent files in this directory to see if this is the newest torrent file available
    * Adds new torrent to transmission-daemon if and only if this is the case
    * Copies original file to transmission folder to allow seeding to take place
    * Monitors torrents for completed downloads
    * Applies update manifests and installs modules as soon as they have finished downloading

**_NOTE_**: lines 79, 84 and 93 contain calls to `os.system` which require the user to enter the username and password information for their transmission client(s).
