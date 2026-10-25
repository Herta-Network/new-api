package system_setting

import "strings"

// ServerAddress is the public address of the backend: the AI API, payment
// callbacks and the files the backend serves are reached there.
var ServerAddress = "http://localhost:3000"

// FrontendAddress is the public address of the console frontend when it is
// deployed apart from the backend; empty when the backend serves it.
var FrontendAddress = ""

var TaskPublicAddress = ""
var WorkerUrl = ""
var WorkerValidKey = ""
var WorkerAllowHttpImageRequestEnabled = false

// ConsoleAddress returns the public address users open the console at:
// FrontendAddress, or ServerAddress when the backend serves the console.
// Links into the console, OAuth callbacks and the default Passkey domain are
// built on it.
func ConsoleAddress() string {
	return ConsoleAddressOf(FrontendAddress, ServerAddress)
}

// ConsoleAddressOf is ConsoleAddress for the given frontend and server
// addresses.
func ConsoleAddressOf(frontendAddress, serverAddress string) string {
	if strings.TrimSpace(frontendAddress) != "" {
		return frontendAddress
	}
	return serverAddress
}

func EnableWorker() bool {
	return WorkerUrl != ""
}
