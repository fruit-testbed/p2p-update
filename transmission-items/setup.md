# Transmission Setup Guide

Transmission is the bitTorrent client used to share and receive updates in this project.

## What's in this repo?

This repo contains the following files:
* **settings.json**
* **lizard.jpg**
* **lizard.torrent**

**settings.json** is the configuration settings for the Transmission bitTorrent client. This template requires the user to fill in the following information:
   * Machine's IPv4 address (`"bind-address-ipv4": "[IP]"`)
   * Peer listening port (`"peer-port": [PORT]`)
   * Client password (`"rpc-password": "[HASH]"` - note that this is entered as plaintext and then overwritten by a SHA-1 hash as soon as transmission-daemon starts)
   * Path for accessing UI (`"rpc-url": "/[URL]/"`)
   * Client username (`"rpc-username": "[USERNAME]"`)
   
`"rpc-whitelist"` already has localhost and typical address space behind most NATs defined, but further IP address ranges may need to be added (eg. on university networks).

These settings can be managed through a UI by visiting `[IP]:9091/[URL]/web/`, but most management of Transmission in this project will be done through altering this file instead (ie. headless setup).

**_NOTE:_** Make sure transmission-daemon is not active using `sudo service transmission-daemon stop` to avoid changes to **settings.json** being instantly overwritten.

**lizard.jpg** is a small image used as test data.

**lizard.torrent** is the torrent file for **lizard.jpg**. Like all torrent files in this project, this does not have any tracker URLs - it is designed to be distributed peer-to-peer without having to rely on a central server.

## Setting up Transmission
To install transmission, use `$sudo apt-get install transmission`. This should install transmission-daemon, transmission-cli, and transmission-common.

Make sure `tranmission-daemon` is inactive by running `$sudo service transmission-daemon stop` before making any changes.

Edit the settings listed above in **settings.json**, then start the `transmission-daemon` service using `$sudo service transmission-daemon start`. 



## Testing the setup
To test if **settings.json** is configured correctly, place **lizard.jpg** and **lizard.torrent** into `/var/lib/transmission-daemon/downloads`. The file itself is required for local data verification, the torrent file is needed to enable seeding through `transmission-remote`. Register the torrent using the command `$transmission-remote -n '[USERNAME]:[PASSWORD]' -a lizard.torrent`. 

Copy the torrent file to another machine also running Transmission and place it in `/var/lib/transmission-daemon/downloads`. Register the torrent with `transmission-remote` as outlined above. The file should start sharing between the two machines soon after.

Status and progress of torrents can be checked from command line using `$transmission-remote -n '[USERNAME]:[PASSWORD]' -l`


## Creating new torrent files
To create a torrent file, use the command `$transmission-create -o /var/lib/transmission-daemon/downloads/[TORRENT_NAME_HERE].torrent /var/lib/transmission-daemon/downloads/[FILE_BEING_SHARED]`
