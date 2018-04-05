package main

const (
	// DefaultTracker is the default BitTorrent tracker address
	DefaultTracker = "https://fruit-testbed.org:443/announce"

	// DefaultPieceLength is the default length of BitTorrent file-piece
	DefaultPieceLength = 32 * 1024
)

const (
	signatureName = "org.fruit-testbed"
	softwareName  = "fruit/p2p-update"

	stunPassword          = "123"
	stunMaxPacketDataSize = 56 * 1024
)

const (
	stateClosed = iota
	stateOpening
	stateOpened
	stateBinding
	stateBindError
	stateListening
	stateProcessingMessage
	stateMessageError
)

const (
	eventOpen = iota + 100
	eventClose
	eventBind
	eventSuccess
	eventError
	eventUnderLimit
	eventOverLimit
	eventChannelExpired
)
