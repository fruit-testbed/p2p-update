# State Machine of Overlay

```mermaid
graph TD;
	closed -->|open| opened;
	opened -->|close| closed;
	opened -->|bind| binding;
	binding -->|error| bindError;
	bindError -->|underLimit| opened;
	bindError -->|overLimit| closed;
	binding -->|success| receivingData;
	receivingData -->|close| closed;
	receivingData -->|success| processingData;
	receivingData -->|error| dataError;
	receivingData -->|channelExpired| binding;
	processingData -->|success| receivingData;
	processingData -->|error| dataError;
	dataError -->|underLimit| receivingData;
	dataError -->|overLimit| binding;
```

