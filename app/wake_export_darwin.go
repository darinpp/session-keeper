package app

import "C"

//export goScreensDidWake
func goScreensDidWake() {
	screensWakeHandler()
}
