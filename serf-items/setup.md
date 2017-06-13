## Serf setup guide

Serf is the management tool used to distribute commands through a swarm in this project.

This repo contains the following files:
   * **serf.conf**
   * **deploy.sh**
   * **slack.sh**

**serf.conf** contains setup information for the node itself. It requires the user to supply:
   * Name for the node (`"node_name": "[NAME]",`)
   * Group of nodes to discover (`"discover": "[PROJECT]",`)
   * IP of the machine (`"bind": "[IP]",`)
   * Datacenter the machine is a part of (`"datacentre": "[CITY]",`)

It also contains one standard event handler, `query`, and two custom event handlers, `deploy` and `slack`. These are defined by **deploy.sh** and **slack.sh**.

**deploy.sh** is a generic script which will be modified to do something productive later on. Currently it just writes a log that the command has been executed to `/tmp/deploy-events`.

**slack.sh** allows a node within the swarm to post messages to a slack group. The template provided requires the user to provide the URL needed to post to a specific channel.
