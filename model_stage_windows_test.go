//go:build windows

package main

import (
	"os"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestPrivateModelStageDirectoryDACL(t *testing.T) {
	directory, err := createPrivateModelStageDirectory()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(directory) }()
	descriptor, err := windows.GetNamedSecurityInfo(
		directory, windows.SE_FILE_OBJECT,
		windows.DACL_SECURITY_INFORMATION,
	)
	if err != nil || descriptor == nil || !descriptor.IsValid() {
		t.Fatalf("模型固定目录安全描述符不可用：%v", err)
	}
	control, _, err := descriptor.Control()
	if err != nil || control&windows.SE_DACL_PROTECTED == 0 {
		t.Fatalf("模型固定目录仍继承 DACL：control=%v err=%v", control, err)
	}
	dacl, _, err := descriptor.DACL()
	if err != nil || dacl == nil {
		t.Fatalf("模型固定目录 DACL 不可用：%v", err)
	}
	currentUser, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		t.Fatal(err)
	}
	localSystem, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
	if err != nil {
		t.Fatal(err)
	}
	currentUserAllowed, localSystemAllowed := false, false
	for index := uint32(0); index < uint32(dacl.AceCount); index++ {
		var ace *windows.ACCESS_ALLOWED_ACE
		if err := windows.GetAce(dacl, index, &ace); err != nil || ace == nil || ace.Header.AceType != windows.ACCESS_ALLOWED_ACE_TYPE {
			t.Fatalf("模型固定目录包含不支持的 ACE：index=%d err=%v", index, err)
		}
		sid := (*windows.SID)(unsafe.Pointer(&ace.SidStart))
		switch {
		case sid.Equals(currentUser.User.Sid):
			currentUserAllowed = true
		case sid.Equals(localSystem):
			localSystemAllowed = true
		default:
			t.Fatalf("模型固定目录向非预期主体授权：%s", sid.String())
		}
	}
	if !currentUserAllowed || !localSystemAllowed {
		t.Fatalf("模型固定目录缺少受信任主体：current_user=%v system=%v", currentUserAllowed, localSystemAllowed)
	}
}
