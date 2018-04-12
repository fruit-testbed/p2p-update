# Diagrams

The following diagrams can be opened with [Typora](https://typora.io)

## State Machine of Overlay

```mermaid
graph TD;
	closed -->|open| opening;
	opening -->|success| opened;
	opening -->|error| closed;
	opened -->|close| closed;
	opened -->|bind| binding;
	binding -->|error| bindError;
	bindError -->|underLimit| opened;
	bindError -->|overLimit| closed;
	binding -->|success| listening;
	listening -->|close| closed;
	listening -->|success| processingMessage;
	listening -->|error| messageError;
	listening -->|channelExpired| binding;
	processingMessage -->|success| listening;
	processingMessage -->|error| messageError;
	messageError -->|underLimit| listening;
	messageError -->|overLimit| binding;
```

## Multi UDP-Packets Message

```sequence
Peer1->Peer2: TID,SendReq
Peer2-->Peer1: TID,SendReq,SendAck
Peer1->Peer2: TID,SendAck,TotalSequences
Peer2-->Peer1: TID,SendReady,TotalSequences
Peer1->Peer2: TID,DataPost,Sequence:1,SequencePayload
Peer1->Peer2: TID,DataPost,Sequence:2,SequencePayload
Peer1->Peer2: ...
Peer1->Peer2: TID,DataPost,Sequence:N,SequencePayload
Peer2-->Peer1: TID,DataError,MissingSequences:10,17
Peer1->Peer2: TID,DataPost,Sequence:10,SequencePayload
Peer1->Peer2: TID,DataPost,Sequence:7,SequencePayload
Peer2-->Peer1: TID,DataSuccess
```



## Update Lifecycle

```mermaid
graph TD;
	created -->|download| downloading;
	created -->|delete| deleted;
	downloading -->|success| downloaded;
	downloading -->|error| downloadError;
	downloading -->|stop| created;
	downloadError -->|underLimit| downloading;
	downloadError -->|overLimit| created;
	downloaded -->|deploy| deploying;
	downloaded -->|delete| deleted;
	downloaded -->|fileRemoved| created;
	deploying -->|success| deployed;
	deploying -->|error| deployError;
	deploying -->|stop| downloaded;
	deployError -->|underLimit| deploying;
	deployError -->|overLimit| downloaded;
	deployed -->|delete| deleted;
	deployed -->|fileRemoved| created;
	deleted -->|create| created;
```
