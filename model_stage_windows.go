//go:build windows

package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

func createPrivateModelStageDirectory() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	descriptor, err := windows.SecurityDescriptorFromString(
		"D:P(A;OICI;FA;;;" + user.User.Sid.String() + ")(A;OICI;FA;;;SY)",
	)
	if err != nil {
		return "", err
	}
	attributes := &windows.SecurityAttributes{
		SecurityDescriptor: descriptor,
	}
	attributes.Length = uint32(unsafe.Sizeof(*attributes))
	for attempt := 0; attempt < 8; attempt++ {
		random := make([]byte, 16)
		if _, err := rand.Read(random); err != nil {
			return "", err
		}
		path := filepath.Join(os.TempDir(), "v-local-cli-sensevoice-model-"+hex.EncodeToString(random))
		pointer, err := windows.UTF16PtrFromString(path)
		if err != nil {
			return "", err
		}
		if err := windows.CreateDirectory(pointer, attributes); err == nil {
			return path, nil
		} else if !errors.Is(err, windows.ERROR_ALREADY_EXISTS) {
			return "", err
		}
	}
	return "", errors.New("无法创建唯一模型固定目录")
}
