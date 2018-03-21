package main

const (
	stateClosed = iota
	stateOpening
	stateOpened
	stateBinding
	stateBindError
	stateListening
	stateProcessingMessage
	stateMessageError

	stateCreated
	stateDownloading
	stateDownloadError
	stateDownloaded
	stateDeploying
	stateDeployed
	stateDeleted
	stateDeployError
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

	eventDownload
	eventStop
	eventDeploy
	eventDelete
	eventCreate
)
