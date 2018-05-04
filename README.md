# Peer-to-Peer Secure Update

This project aims to provide a framework to securely distribute system update
using peer-to-peer procotol that works in heterogeneous network environment,
in the presence of NATs and firewalls, where there is no necessarily direct
access from a management node to the devices being updated.

The framework combines several key techniques:
1. STUN-based UDP hole punching to discover and open NAT bindings
2. A gossip protocol to deliver short messages to distribute update notifications
3. BitTorrent to securely distribute the software update

This project is part of Federated RaspberryPi micro-Infrastructure Testbed - [FRuIT](https://fruit-testbed.org).


## To build

Requirements:
- Go version >=1.9
- dep (https://github.com/golang/dep)

```
cd p2p-update
dep ensure
./build
```

This generates an executable binary file: `p2pupdate`.


## To run

The application can run on any Linux operating system.


### Server mode

The server mode runs a lightweight STUN service to bootstrap a new peer and advertise
its session to existing peers. Both the update notification and file are distributed
using peer-to-peer protocols.

To run in server mode, invoke:
```
./p2pupdate server
```


## Agent mode

The agent mode performs peer-to-peer communications with other agents to send/receive
update notifications, use it to download/serve the update files, and deploy the updates
on local node.

To run in agent mode, invoke:
```
./p2pupdate agent
```

There are two options
- `--config-file`, is used to pass a custom config file.
- `--default-config`, prints default configuration to standard output.



## License

Apache Version 2.0.
