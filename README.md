# p2p-update
Peer to Peer Update project

## Setup guides
* [Transmission](https://github.com/fruit-testbed/p2p-update/blob/master/transmission-items/setup.md "Transmission setup guide")
* [Serf](https://github.com/fruit-testbed/p2p-update/blob/master/serf-items/setup.md "Serf setup guide")
* [Puppet](https://github.com/fruit-testbed/p2p-update/blob/master/puppet-items/setup.md "Puppet setup guide")

## Sending torrent files over Serf

**.torrent** files can be sent as a user event payload to other nodes using Serf. This is done using the following command:

``./serf event update "$`cat [FILE].torrent`"``.

These payloads are currently stored in **~/received-torrent.torrent**.

Note that Serf user events have a 512 byte limit. Torrent files of just over 200 bytes can be reliably transmitted - further work is being done to establish the exact limit on this, and to hopefully increase this limit.

## Managing new torrent files

**agent.py** is a management script to automate the process of torrenting and applying new update files for each node within the swarm. To run this script, use:
`$sudo python agent.py`

Currently **agent.py** writes the received data to a torrent file in the required transmission directory, then adds this new torrent file to the transmission daemon. It will eventually be linked to Serf to actively listen for update events, check the version of the new torrent, and apply updates once the full files have finished downloading.

**_NOTE_**: lines 36 and 38 contain calls to `os.system` which require the user to enter the username and password information for their transmission client(s).
