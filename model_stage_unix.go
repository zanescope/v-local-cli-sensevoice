//go:build !windows

package main

import "os"

func createPrivateModelStageDirectory() (string, error) {
	return os.MkdirTemp("", "v-local-cli-sensevoice-model-*")
}
