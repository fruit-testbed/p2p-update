# Serf Setup Guide

Serf is the management tool used to distribute commands through a swarm in this project.

## What's in this repo?

This repo contains the following files:
   * **serf.conf**
   * **deploy.sh**
   * **slack.sh**

**serf.conf** contains setup information for the node itself. It requires the user to supply:
   * Name for the node (`"node_name": "[NAME]",`)
   * Group of nodes to discover (`"discover": "[PROJECT]",`)
   * IP of the machine (`"bind": "[IP]",`)
   * IP used for remote procedure calls (`"rpc-addr": "[IP]",` - at least one member in the swarm needs to have this defined but it should be present on all nodes in a peer-to-peer swarm to prevent substantial reliance on a single machine)
   * Datacenter the machine is a part of (`"datacentre": "[CITY]",`)

It also contains one standard event handler, `query`, and two custom event handlers, `deploy` and `slack`. These are defined by **deploy.sh** and **slack.sh**.

**deploy.sh** is a generic script which will be modified to do something productive later on. Currently it just writes a log that the command has been executed to `/tmp/deploy-events`.

**slack.sh** allows a node within the swarm to post messages to a slack group. The template provided requires the user to provide the URL needed to post to a specific channel.

## Setting up Serf

To install Serf, run the command `$wget https://releases.hashicorp.com/serf/0.8.1/serf_0.8.1_linux_arm.zip` - this is correct for a Raspberry Pi target at time of writing (June 2017), adjust version number and target OS as required.

Unzip this file to **/usr/local/sbin** and fill in the information listed above in the **serf.conf** template.

To start the first node in the swarm, use the command `$serf agent -config-file serf.conf`.

Nodes can join an existing swarm by using `$serf agent -config-file serf.conf -join [IP]`, where `[IP]` is the valid address of any machine which is a current participant.

Open a new terminal and run the command `$serf members`. If all members show up as 'alive' with correct tag descriptions, the setup process has been successful.

## Queries and events

Currently **serf.conf** only contains one query. This can be executed using `$serf query load`, and will return the uptime, number of users, and load average of each node. This repo will contain more complex query examples as this project continues.

Agents in a swarm can trigger events using `$serf event [name] [parameters]`, where `[name]` is the string specified in **serf.conf**'s event handlers (ie. `"user:[name]=[path_to_somescript.sh]"`). New custom events can be easily added by supplying more event handlers in this format to **serf.conf**.
**_NOTE_**: remember to run `$sudo chmod +x [script]` so serf nodes can actually execute it. 

`$serf event deploy [string]` does nothing other than write `[string]` to `/tmp/deploy-events`. The log actively listening for events does register that a node executed this script.

`$serf event slack "[message]"` posts `[message]` to a slack channel and records what was sent in `/tmp/slack-messages`. The slack channel to post to is specified by placing a custom link in the `curl` command in **slack.sh**.

## Encryption

To enable encrypted communication within a swarm, add the key-value pair `"encrypt_key": "[key]"` to **serf.conf**. `[key]` is generated using `$serf keygen`.

All members within a cluster must have the same encryption key to be able to communicate. This provides a layer of security to prevent unauthorised parties joining the swarm, but also has the additional benefit of preventing nodes joining the wrong swarm by accident.

Key management commands can be run from any active node within the swarm:
   * `serf keys -install=[key]` creates a new key
   * `serf keys -use=[key]` changes the primary key being used
   * `serf keys -remove=[key]` removes the specified key
   * `serf keys -list` lists all installed keys

These commands will apply changes to all nodes currently participating in the swarm.
