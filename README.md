# p2p-update
Peer to Peer Update project

## Setup guides
* [Transmission](https://github.com/fruit-testbed/p2p-update/blob/master/transmission-items/setup.md "Transmission setup guide")
* [Serf](https://github.com/fruit-testbed/p2p-update/blob/master/serf-items/setup.md "Serf setup guide")
* [Puppet](https://github.com/fruit-testbed/p2p-update/blob/master/puppet-items/setup.md "Puppet setup guide")

## Sending torrent files over Serf
T
**.torrent** files can be sent as a user event payload to other nodes using Serf. This is done using the following command:
`./serf event update "$\`cat [FILE].torrent\`"`.
These payloads are currently stored in `~/received-torrent.torrent`.
Note that Serf user events have a 512 byte limit. Torrent files of just over 200 bytes can be reliably transmitted - further work is being done to establish the exact limit on this, and to hopefully increase this limit.
